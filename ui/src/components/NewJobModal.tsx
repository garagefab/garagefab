import { useState, FormEvent, useEffect } from 'react';
import { X, PlusCircle, Loader2, AlertCircle } from 'lucide-react';
import { apiFetch } from '../lib/api';
import { Job, Project, WorkType } from '../types/api';

interface NewJobModalProps {
  isOpen: boolean;
  projects: Project[];
  onClose: () => void;
  onJobCreated: (job: Job) => void;
}

export function NewJobModal({ isOpen, projects, onClose, onJobCreated }: NewJobModalProps) {
  const [projectId, setProjectId] = useState<number | ''>('');
  const [workType, setWorkType] = useState<WorkType>('feature');
  const [title, setTitle] = useState('');
  const [intent, setIntent] = useState('');
  const [loading, setLoading] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [generalError, setGeneralError] = useState<string | null>(null);

  // Set default project on open
  useEffect(() => {
    if (isOpen) {
      const activeProjects = projects.filter((p) => !p.is_archived);
      if (activeProjects.length > 0 && projectId === '') {
        setProjectId(activeProjects[0].id);
      }
      setFieldErrors({});
      setGeneralError(null);
    } else {
      setTitle('');
      setIntent('');
    }
  }, [isOpen, projects]);

  // Handle Escape key to close
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();

    setFieldErrors({});
    setGeneralError(null);

    const errors: Record<string, string> = {};
    if (!projectId) {
      errors.project_id = 'Please select a project';
    }
    if (!title.trim()) {
      errors.title = 'Title is required';
    }
    if (!intent.trim()) {
      errors.intent = 'Intent description is required';
    }

    if (Object.keys(errors).length > 0) {
      setFieldErrors(errors);
      return;
    }

    setLoading(true);

    try {
      const newJob = await apiFetch<Job>('/api/jobs', {
        method: 'POST',
        body: JSON.stringify({
          project_id: Number(projectId),
          work_type: workType,
          title: title.trim(),
          intent: intent.trim(),
        }),
      });

      onJobCreated(newJob);
      onClose();
    } catch (err: any) {
      if (err?.details) {
        setFieldErrors(err.details);
      } else {
        setGeneralError(err?.message || 'Failed to create job');
      }
    } finally {
      setLoading(false);
    }
  };

  const activeProjects = projects.filter((p) => !p.is_archived);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="new-job-title"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm"
    >
      <div className="w-full max-w-lg rounded-2xl border border-slate-800 bg-slate-900 shadow-2xl p-6 relative">
        <div className="flex items-center justify-between pb-4 border-b border-slate-800">
          <div className="flex items-center gap-2.5">
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
              <PlusCircle className="h-5 w-5" />
            </div>
            <div>
              <h2 id="new-job-title" className="text-base font-semibold text-white">
                Create New Job
              </h2>
              <p className="text-xs text-slate-400">Submit intent into the SDLC factory pipeline (INT-1)</p>
            </div>
          </div>

          <button
            onClick={onClose}
            aria-label="Close modal"
            className="rounded-lg p-1.5 text-slate-400 hover:text-white hover:bg-slate-800 transition-colors"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="mt-5 space-y-4">
          {generalError && (
            <div role="alert" className="flex items-center gap-2 rounded-xl border border-rose-500/30 bg-rose-500/10 p-3 text-xs text-rose-300">
              <AlertCircle className="h-4 w-4 shrink-0 text-rose-400" />
              <span>{generalError}</span>
            </div>
          )}

          {/* Project & Work Type in a grid */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
            <div>
              <label htmlFor="job-project" className="block text-xs font-medium text-slate-300 mb-1">
                Project <span className="text-rose-400">*</span>
              </label>
              <select
                id="job-project"
                value={projectId}
                onChange={(e) => setProjectId(e.target.value === '' ? '' : Number(e.target.value))}
                disabled={loading || activeProjects.length === 0}
                className="w-full rounded-xl border border-slate-700 bg-slate-950 px-3.5 py-2 text-sm text-white focus:outline-none focus:ring-2 focus:ring-indigo-500/30 cursor-pointer"
              >
                {activeProjects.length === 0 ? (
                  <option value="">No registered projects</option>
                ) : (
                  activeProjects.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}
                    </option>
                  ))
                )}
              </select>
              {fieldErrors.project_id && (
                <p className="mt-1 text-xs text-rose-400">{fieldErrors.project_id}</p>
              )}
            </div>

            <div>
              <label htmlFor="job-work-type" className="block text-xs font-medium text-slate-300 mb-1">
                Work Type
              </label>
              <select
                id="job-work-type"
                value={workType}
                onChange={(e) => setWorkType(e.target.value as WorkType)}
                disabled={loading}
                className="w-full rounded-xl border border-slate-700 bg-slate-950 px-3.5 py-2 text-sm text-white focus:outline-none focus:ring-2 focus:ring-indigo-500/30 cursor-pointer"
              >
                <option value="feature">Feature</option>
                <option value="refactor">Refactor</option>
                <option value="bug_fix">Bug Fix</option>
                <option value="docs">Docs</option>
              </select>
            </div>
          </div>

          {/* Title Field with Inline Validation (UI-6) */}
          <div>
            <label htmlFor="job-title" className="block text-xs font-medium text-slate-300 mb-1">
              Title <span className="text-rose-400">*</span>
            </label>
            <input
              id="job-title"
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="e.g. Add OAuth login support"
              disabled={loading}
              className={`w-full rounded-xl border ${
                fieldErrors.title ? 'border-rose-500/80 bg-rose-500/5' : 'border-slate-700 bg-slate-950'
              } px-3.5 py-2 text-sm text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/30`}
            />
            {fieldErrors.title && (
              <p className="mt-1 text-xs text-rose-400 flex items-center gap-1">
                <AlertCircle className="h-3 w-3 shrink-0" />
                {fieldErrors.title}
              </p>
            )}
          </div>

          {/* Intent Description with Inline Validation */}
          <div>
            <label htmlFor="job-intent" className="block text-xs font-medium text-slate-300 mb-1">
              Intent / Requirements <span className="text-rose-400">*</span>
            </label>
            <textarea
              id="job-intent"
              rows={4}
              value={intent}
              onChange={(e) => setIntent(e.target.value)}
              placeholder="Describe the desired outcome, requirements, or problem to solve in markdown..."
              disabled={loading}
              className={`w-full rounded-xl border ${
                fieldErrors.intent ? 'border-rose-500/80 bg-rose-500/5' : 'border-slate-700 bg-slate-950'
              } px-3.5 py-2 text-sm text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/30 font-sans`}
            />
            {fieldErrors.intent && (
              <p className="mt-1 text-xs text-rose-400 flex items-center gap-1">
                <AlertCircle className="h-3 w-3 shrink-0" />
                {fieldErrors.intent}
              </p>
            )}
          </div>

          <div className="flex items-center justify-end gap-3 pt-3 border-t border-slate-800">
            <button
              type="button"
              onClick={onClose}
              className="rounded-xl px-4 py-2 text-xs font-medium text-slate-400 hover:text-white hover:bg-slate-800 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading || activeProjects.length === 0}
              className="flex items-center gap-2 rounded-xl bg-indigo-600 px-4 py-2 text-xs font-semibold text-white shadow-md shadow-indigo-600/30 hover:bg-indigo-500 disabled:opacity-50 transition-colors cursor-pointer"
            >
              {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Submit Job (01_Intent)
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
