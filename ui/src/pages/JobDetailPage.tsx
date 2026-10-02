import { useEffect, useState, useCallback } from 'react';
import {
  ArrowLeft,
  GitBranch,
  FolderGit2,
  GitPullRequest,
  RefreshCw,
  AlertTriangle,
  FileText,
  ShieldCheck,
  FileDiff,
  HelpCircle,
} from 'lucide-react';
import { Job, StepRun, EvidenceSummary } from '../types/api';
import { apiFetch, apiFetchText } from '../lib/api';
import { Link, useRouter } from '../lib/router';
import { JobActions } from '../components/JobActions';
import { PipelineVisualizer } from '../components/PipelineVisualizer';
import { ClarificationForm } from '../components/ClarificationForm';
import { MarkdownViewer } from '../components/MarkdownViewer';
import { EvidenceSection } from '../components/EvidenceSection';
import { DiffViewer } from '../components/DiffViewer';

interface JobDetailPageProps {
  jobId: number;
  onOpenLogs?: (step: StepRun) => void;
}

export function JobDetailPage({ jobId, onOpenLogs }: JobDetailPageProps) {
  const { navigate } = useRouter();

  const [job, setJob] = useState<Job | null>(null);
  const [stepRuns, setStepRuns] = useState<StepRun[]>([]);
  const [specContent, setSpecContent] = useState<string | null>(null);
  const [evidence, setEvidence] = useState<EvidenceSummary | null>(null);
  const [diff, setDiff] = useState<string | null>(null);

  const [activeTab, setActiveTab] = useState<'spec' | 'evidence' | 'diff' | 'intent'>('spec');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadJobData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);

      // 1. Fetch core job and steps
      const [jobData, stepsData] = await Promise.all([
        apiFetch<Job>(`/api/jobs/${jobId}`),
        apiFetch<StepRun[]>(`/api/jobs/${jobId}/steps`).catch(() => []),
      ]);

      setJob(jobData);
      setStepRuns(stepsData);

      // 2. Fetch spec if stage is 02_Clarification_and_Spec or beyond (SPC-8)
      try {
        const specRaw = await apiFetchText(`/api/jobs/${jobId}/artifacts/spec`);
        setSpecContent(specRaw);
      } catch {
        setSpecContent(null);
      }

      // 3. Fetch evidence if available
      try {
        const evData = await apiFetch<EvidenceSummary>(`/api/jobs/${jobId}/evidence`);
        setEvidence(evData);
      } catch {
        setEvidence(null);
      }

      // 4. Fetch diff if available
      try {
        const diffRaw = await apiFetchText(`/api/jobs/${jobId}/diff`);
        setDiff(diffRaw);
      } catch {
        setDiff(null);
      }
    } catch (err: any) {
      setError(err?.message || 'Failed to load task details');
    } finally {
      setLoading(false);
    }
  }, [jobId]);

  useEffect(() => {
    loadJobData();
  }, [loadJobData]);

  // Action handlers calling backend REST endpoints (UI-4)
  const handleApprove = async () => {
    await apiFetch(`/api/jobs/${jobId}/approve`, {
      method: 'POST',
      body: JSON.stringify({ head_sha: job?.head_sha || '' }),
    });
    await loadJobData();
  };

  const handleReject = async (note: string) => {
    await apiFetch(`/api/jobs/${jobId}/reject`, {
      method: 'POST',
      body: JSON.stringify({ note }),
    });
    await loadJobData();
  };

  const handleRetry = async () => {
    await apiFetch(`/api/jobs/${jobId}/retry`, {
      method: 'POST',
    });
    await loadJobData();
  };

  const handleCancel = async () => {
    await apiFetch(`/api/jobs/${jobId}/cancel`, {
      method: 'POST',
    });
    await loadJobData();
  };

  if (loading && !job) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center">
        <div className="flex items-center gap-3 text-slate-400">
          <RefreshCw className="h-5 w-5 animate-spin text-indigo-400" />
          <span>Loading job details...</span>
        </div>
      </div>
    );
  }

  if (error || !job) {
    return (
      <div className="p-8 max-w-xl mx-auto">
        <div
          role="alert"
          className="rounded-2xl border border-rose-500/30 bg-rose-500/10 p-6 text-rose-300 space-y-4"
        >
          <div className="flex items-center gap-3">
            <AlertTriangle className="h-6 w-6 text-rose-400 shrink-0" />
            <div>
              <h3 className="font-semibold text-rose-200">Unable to load job</h3>
              <p className="text-sm mt-0.5">{error || 'Job not found'}</p>
            </div>
          </div>
          <button
            onClick={() => navigate('/board')}
            className="inline-flex items-center gap-2 rounded-xl bg-slate-800 px-4 py-2 text-xs font-semibold text-white hover:bg-slate-700 transition-colors"
          >
            <ArrowLeft className="h-4 w-4" />
            Back to Board
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6 lg:p-8 max-w-7xl mx-auto">
      {/* Top Navigation & Header */}
      <div className="flex flex-col gap-4 border-b border-slate-800 pb-5">
        <div className="flex items-center justify-between">
          <Link
            href="/board"
            className="inline-flex items-center gap-1.5 text-xs font-medium text-slate-400 hover:text-white transition-colors"
          >
            <ArrowLeft className="h-4 w-4" />
            Back to Project Board
          </Link>

          <button
            onClick={loadJobData}
            title="Refresh job state"
            className="rounded-xl border border-slate-700 bg-slate-900 p-2 text-slate-400 hover:text-white hover:bg-slate-800 transition-colors cursor-pointer"
          >
            <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin text-indigo-400' : ''}`} />
          </button>
        </div>

        <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
          <div>
            <div className="flex items-center gap-2.5 flex-wrap">
              <span className="font-mono text-sm font-semibold text-indigo-400">#{job.id}</span>
              <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-white">{job.title}</h1>
              <span className="rounded-full bg-slate-800 px-2.5 py-0.5 text-xs font-medium text-slate-300 border border-slate-700">
                {job.work_type}
              </span>
            </div>

            <div className="mt-2 flex flex-wrap items-center gap-4 text-xs text-slate-400">
              <span className="flex items-center gap-1">
                <FolderGit2 className="h-3.5 w-3.5 text-slate-500" />
                Project: {job.project_name || `#${job.project_id}`}
              </span>

              {job.branch_name && (
                <span className="flex items-center gap-1 font-mono">
                  <GitBranch className="h-3.5 w-3.5 text-slate-500" />
                  {job.branch_name}
                </span>
              )}

              {job.pr_url && (
                <a
                  href={job.pr_url}
                  target="_blank"
                  rel="noreferrer"
                  className="flex items-center gap-1 text-indigo-400 hover:underline"
                >
                  <GitPullRequest className="h-3.5 w-3.5" />
                  View Pull Request
                </a>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Action Bar (UI-4, HND-1, APR-5..7) */}
      <JobActions
        job={job}
        onApprove={handleApprove}
        onReject={handleReject}
        onRetry={handleRetry}
        onCancel={handleCancel}
      />

      {/* SDLC Pipeline Visualizer (UI-3) */}
      <PipelineVisualizer job={job} stepRuns={stepRuns} onOpenLogs={onOpenLogs} />

      {/* Clarification Questionnaire Form if in needs_clarification (SPC-3) */}
      {job.status === 'needs_clarification' && (
        <ClarificationForm jobId={job.id} onSubmitted={loadJobData} />
      )}

      {/* Artifact Drill-down Tabs */}
      <div className="rounded-2xl border border-slate-800 bg-slate-900/60 shadow-lg overflow-hidden">
        {/* Tab Navigation */}
        <div className="flex items-center gap-1 px-4 pt-3 bg-slate-950 border-b border-slate-800 overflow-x-auto">
          <button
            type="button"
            onClick={() => setActiveTab('spec')}
            className={`flex items-center gap-2 px-4 py-2 text-xs font-semibold rounded-t-xl transition-colors cursor-pointer ${
              activeTab === 'spec'
                ? 'bg-slate-900 text-white border-t border-x border-slate-800'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <FileText className="h-3.5 w-3.5" />
            <span>Job Spec (spec.md)</span>
            {specContent && <span className="h-1.5 w-1.5 rounded-full bg-indigo-400" />}
          </button>

          <button
            type="button"
            onClick={() => setActiveTab('evidence')}
            className={`flex items-center gap-2 px-4 py-2 text-xs font-semibold rounded-t-xl transition-colors cursor-pointer ${
              activeTab === 'evidence'
                ? 'bg-slate-900 text-white border-t border-x border-slate-800'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <ShieldCheck className="h-3.5 w-3.5" />
            <span>Evidence</span>
            {evidence && <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />}
          </button>

          <button
            type="button"
            onClick={() => setActiveTab('diff')}
            className={`flex items-center gap-2 px-4 py-2 text-xs font-semibold rounded-t-xl transition-colors cursor-pointer ${
              activeTab === 'diff'
                ? 'bg-slate-900 text-white border-t border-x border-slate-800'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <FileDiff className="h-3.5 w-3.5" />
            <span>Unified Diff</span>
            {diff && <span className="h-1.5 w-1.5 rounded-full bg-teal-400" />}
          </button>

          <button
            type="button"
            onClick={() => setActiveTab('intent')}
            className={`flex items-center gap-2 px-4 py-2 text-xs font-semibold rounded-t-xl transition-colors cursor-pointer ${
              activeTab === 'intent'
                ? 'bg-slate-900 text-white border-t border-x border-slate-800'
                : 'text-slate-400 hover:text-white'
            }`}
          >
            <HelpCircle className="h-3.5 w-3.5" />
            <span>Original Intent</span>
          </button>
        </div>

        {/* Tab Content Panes */}
        <div className="p-6">
          {activeTab === 'spec' && (
            <div>
              {specContent ? (
                <MarkdownViewer content={specContent} />
              ) : (
                <div className="p-6 text-center text-xs text-slate-400">
                  Spec has not been generated yet. It will be drafted when the job reaches 02_Clarification_and_Spec.
                </div>
              )}
            </div>
          )}

          {activeTab === 'evidence' && (
            <div>
              {evidence ? (
                <EvidenceSection evidence={evidence} onViewDiff={() => setActiveTab('diff')} />
              ) : (
                <div className="p-6 text-center text-xs text-slate-400">
                  Verification evidence is compiled during the review stage.
                </div>
              )}
            </div>
          )}

          {activeTab === 'diff' && (
            <div>
              <DiffViewer diff={diff || ''} />
            </div>
          )}

          {activeTab === 'intent' && (
            <div className="space-y-4">
              <h3 className="text-xs font-semibold text-slate-400 uppercase tracking-wider">
                Submitted Intent Requirements (01_Intent)
              </h3>
              <MarkdownViewer content={job.intent || 'No intent provided.'} />
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
