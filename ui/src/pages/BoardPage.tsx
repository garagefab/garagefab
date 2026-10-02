import { useEffect, useState, useCallback } from 'react';
import { RefreshCw, AlertTriangle, PlusCircle, ArrowLeft } from 'lucide-react';
import { Job, Project } from '../types/api';
import { apiFetch } from '../lib/api';
import { KanbanColumn } from '../components/KanbanColumn';
import { ProjectFilter } from '../components/ProjectFilter';
import { Link } from '../lib/router';
import { useSSE } from '../hooks/useSSE';

interface BoardPageProps {
  onOpenNewJob: () => void;
}

interface ColumnDef {
  id: string;
  title: string;
  subtitle: string;
  filterFn: (job: Job) => boolean;
}

const COLUMNS: ColumnDef[] = [
  {
    id: '01_Intent',
    title: '01 Intent',
    subtitle: 'Queue & Intake',
    filterFn: (job) => job.stage === '01_Intent',
  },
  {
    id: '02_Clarification',
    title: '02 Clarification',
    subtitle: 'Q&A questionnaire',
    filterFn: (job) => job.stage === '02_Clarification_and_Spec' && job.status === 'needs_clarification',
  },
  {
    id: '03_Spec',
    title: '03 Spec',
    subtitle: 'Authoring & Review',
    filterFn: (job) =>
      job.stage === '02_Clarification_and_Spec' && job.status !== 'needs_clarification',
  },
  {
    id: '04_Coding',
    title: '04 Coding',
    subtitle: 'Agent & Repair Loop',
    filterFn: (job) => job.stage === '04_Coding',
  },
  {
    id: '05_Review',
    title: '05 Review',
    subtitle: 'Independent session',
    filterFn: (job) => job.stage === '05_Independent_Review',
  },
  {
    id: '06_Approval',
    title: '06 Approval',
    subtitle: 'Human sign-off gate',
    filterFn: (job) => job.stage === '06_Human_Approval_Gate',
  },
  {
    id: '07_Delivered',
    title: '07 Delivered',
    subtitle: 'Completed & PR ready',
    filterFn: (job) => job.stage === '07_Done' || job.status === 'done',
  },
];

export function BoardPage({ onOpenNewJob }: BoardPageProps) {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [selectedProjectId, setSelectedProjectId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);

      const [jobsData, projectsData] = await Promise.all([
        apiFetch<Job[]>('/api/jobs'),
        apiFetch<Project[]>('/api/projects'),
      ]);

      // Enrich jobs with project names
      const projMap = new Map<number, string>();
      projectsData.forEach((p) => projMap.set(p.id, p.name));
      const enrichedJobs = jobsData.map((j) => ({
        ...j,
        project_name: projMap.get(j.project_id) || `Project ${j.project_id}`,
      }));

      setJobs(enrichedJobs);
      setProjects(projectsData);
    } catch (err: any) {
      setError(err?.message || 'Failed to load project board');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadData();
  }, [loadData]);

  // Live SSE listener: auto-update kanban board on pipeline events (UI-5)
  useSSE(loadData);

  const filteredJobs = selectedProjectId
    ? jobs.filter((j) => j.project_id === selectedProjectId)
    : jobs;

  return (
    <div className="flex flex-col h-[calc(100vh-65px)] overflow-hidden">
      {/* Board Header Bar */}
      <div className="flex items-center justify-between px-6 py-4 border-b border-slate-800 bg-slate-950/60 backdrop-blur shrink-0">
        <div className="flex items-center gap-4">
          <Link
            href="/"
            className="flex items-center gap-1.5 text-xs font-medium text-slate-400 hover:text-white transition-colors"
          >
            <ArrowLeft className="h-4 w-4" />
            Overview
          </Link>
          <div className="h-4 w-px bg-slate-800" />
          <h1 className="text-lg font-bold text-white tracking-tight">Project Board</h1>
          <span className="rounded-full bg-slate-800/80 px-2.5 py-0.5 text-xs font-mono text-slate-400 border border-slate-700">
            {filteredJobs.length} jobs
          </span>
        </div>

        <div className="flex items-center gap-3">
          <ProjectFilter
            projects={projects}
            selectedProjectId={selectedProjectId}
            onSelectProject={setSelectedProjectId}
          />

          <button
            onClick={loadData}
            title="Refresh board"
            aria-label="Refresh board"
            className="rounded-xl border border-slate-700 bg-slate-900 p-2 text-slate-400 hover:text-white hover:bg-slate-800 transition-colors cursor-pointer"
          >
            <RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin text-indigo-400' : ''}`} />
          </button>

          <button
            onClick={onOpenNewJob}
            className="inline-flex items-center gap-1.5 rounded-xl bg-indigo-600 px-3 py-1.5 text-xs font-semibold text-white shadow-md shadow-indigo-600/30 hover:bg-indigo-500 transition-colors cursor-pointer"
          >
            <PlusCircle className="h-3.5 w-3.5" />
            New Job
          </button>
        </div>
      </div>

      {/* Kanban Board Horizontal Scroll Container */}
      <div className="flex-1 overflow-x-auto p-6 bg-slate-950">
        {error ? (
          <div
            role="alert"
            className="rounded-2xl border border-rose-500/30 bg-rose-500/10 p-6 text-rose-300 max-w-lg mx-auto flex items-center justify-between"
          >
            <div className="flex items-center gap-3">
              <AlertTriangle className="h-5 w-5 text-rose-400" />
              <span>{error}</span>
            </div>
            <button
              onClick={loadData}
              className="rounded-lg bg-rose-500/20 px-3 py-1 text-xs font-semibold text-rose-200 hover:bg-rose-500/30"
            >
              Retry
            </button>
          </div>
        ) : (
          <div className="flex items-start gap-4 pb-4 min-w-max">
            {COLUMNS.map((col) => {
              const colJobs = filteredJobs.filter(col.filterFn);
              return (
                <KanbanColumn
                  key={col.id}
                  id={col.id}
                  title={col.title}
                  subtitle={col.subtitle}
                  jobs={colJobs}
                />
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
