import { useState, FormEvent, useEffect } from 'react';
import { X, FolderGit2, Loader2, AlertCircle, FileCode, CheckCircle2 } from 'lucide-react';
import { apiFetch } from '../lib/api';
import { Project } from '../types/api';

interface AddProjectModalProps {
  isOpen: boolean;
  onClose: () => void;
  onProjectAdded: (project: Project) => void;
}

export function AddProjectModal({ isOpen, onClose, onProjectAdded }: AddProjectModalProps) {
  const [repoPath, setRepoPath] = useState('');
  const [name, setName] = useState('');
  const [baseRef, setBaseRef] = useState('origin/main');
  const [loading, setLoading] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [generalError, setGeneralError] = useState<string | null>(null);
  const [missingConfigTemplate, setMissingConfigTemplate] = useState<string | null>(null);
  const [generatingTemplate, setGeneratingTemplate] = useState(false);

  useEffect(() => {
    if (!isOpen) {
      setRepoPath('');
      setName('');
      setBaseRef('origin/main');
      setFieldErrors({});
      setGeneralError(null);
      setMissingConfigTemplate(null);
    }
  }, [isOpen]);

  // Handle Escape key to close modal
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

  const handleSubmit = async (e?: FormEvent) => {
    if (e) e.preventDefault();

    setFieldErrors({});
    setGeneralError(null);
    setMissingConfigTemplate(null);

    if (!repoPath.trim()) {
      setFieldErrors({ repo_path: 'Repository path is required' });
      return;
    }

    setLoading(true);

    try {
      const newProj = await apiFetch<Project>('/api/projects', {
        method: 'POST',
        body: JSON.stringify({
          repo_path: repoPath.trim(),
          name: name.trim() || undefined,
          base_ref: baseRef.trim() || 'origin/main',
        }),
      });

      onProjectAdded(newProj);
      onClose();
    } catch (err: any) {
      if (err?.status === 422) {
        // Missing or invalid project.yaml (PRJ-3, UI-6)
        if (err.details) {
          setFieldErrors(err.details);
        }
        // If template was provided in error payload or missing project.yaml
        setMissingConfigTemplate(err.message || 'project.yaml is missing');
      } else if (err?.details) {
        setFieldErrors(err.details);
      } else {
        setGeneralError(err?.message || 'Failed to register project');
      }
    } finally {
      setLoading(false);
    }
  };

  const handleGenerateTemplate = async () => {
    setGeneratingTemplate(true);
    try {
      await apiFetch('/api/projects/config-template', {
        method: 'POST',
        body: JSON.stringify({ repo_path: repoPath.trim() }),
      });
      setMissingConfigTemplate(null);
      // Auto re-validate and register
      await handleSubmit();
    } catch (err: any) {
      setGeneralError('Failed to generate template: ' + err.message);
    } finally {
      setGeneratingTemplate(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="add-project-title"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm"
    >
      <div className="w-full max-w-lg rounded-2xl border border-slate-800 bg-slate-900 shadow-2xl p-6 relative">
        <div className="flex items-center justify-between pb-4 border-b border-slate-800">
          <div className="flex items-center gap-2.5">
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
              <FolderGit2 className="h-5 w-5" />
            </div>
            <div>
              <h2 id="add-project-title" className="text-base font-semibold text-white">
                Add Local Project
              </h2>
              <p className="text-xs text-slate-400">Register a local Git repository with Garagefab</p>
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

          {/* Repo Path Field with Inline Validation (UI-6, PRJ-1..2) */}
          <div>
            <label htmlFor="repo-path" className="block text-xs font-medium text-slate-300 mb-1">
              Repository Path <span className="text-rose-400">*</span>
            </label>
            <input
              id="repo-path"
              type="text"
              value={repoPath}
              onChange={(e) => setRepoPath(e.target.value)}
              placeholder="/Users/you/projects/my-repo"
              disabled={loading}
              className={`w-full rounded-xl border ${
                fieldErrors.repo_path ? 'border-rose-500/80 bg-rose-500/5' : 'border-slate-700 bg-slate-950'
              } px-3.5 py-2 text-sm text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/30 font-mono`}
            />
            {fieldErrors.repo_path && (
              <p className="mt-1 text-xs text-rose-400 flex items-center gap-1">
                <AlertCircle className="h-3 w-3 shrink-0" />
                {fieldErrors.repo_path}
              </p>
            )}
          </div>

          {/* Missing project.yaml template banner (PRJ-3, PRJ-7) */}
          {missingConfigTemplate && (
            <div className="rounded-xl border border-amber-500/30 bg-amber-500/10 p-3.5 space-y-2">
              <div className="flex items-start gap-2 text-xs text-amber-300">
                <FileCode className="h-4 w-4 shrink-0 text-amber-400 mt-0.5" />
                <div>
                  <span className="font-semibold text-amber-200">Missing configuration:</span>
                  <p className="mt-0.5">
                    This repository does not have a <code className="font-mono bg-slate-900 px-1 py-0.5 rounded text-amber-300">.garagefab/project.yaml</code> file.
                  </p>
                </div>
              </div>
              <button
                type="button"
                onClick={handleGenerateTemplate}
                disabled={generatingTemplate}
                className="w-full flex items-center justify-center gap-2 rounded-lg bg-amber-500/20 border border-amber-500/30 px-3 py-1.5 text-xs font-semibold text-amber-200 hover:bg-amber-500/30 transition-colors cursor-pointer"
              >
                {generatingTemplate ? (
                  <>
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    Writing template...
                  </>
                ) : (
                  <>
                    <CheckCircle2 className="h-3.5 w-3.5" />
                    Create .garagefab/project.yaml template
                  </>
                )}
              </button>
            </div>
          )}

          {/* Optional Name */}
          <div>
            <label htmlFor="project-name" className="block text-xs font-medium text-slate-300 mb-1">
              Project Display Name <span className="text-slate-400 font-normal">(optional, defaults to directory name)</span>
            </label>
            <input
              id="project-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. backend-service"
              disabled={loading}
              className="w-full rounded-xl border border-slate-700 bg-slate-950 px-3.5 py-2 text-sm text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/30"
            />
          </div>

          {/* Optional Base Ref */}
          <div>
            <label htmlFor="base-ref" className="block text-xs font-medium text-slate-300 mb-1">
              Base Branch <span className="text-slate-400 font-normal">(default: origin/main)</span>
            </label>
            <input
              id="base-ref"
              type="text"
              value={baseRef}
              onChange={(e) => setBaseRef(e.target.value)}
              placeholder="origin/main"
              disabled={loading}
              className="w-full rounded-xl border border-slate-700 bg-slate-950 px-3.5 py-2 text-sm text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/30 font-mono"
            />
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
              disabled={loading}
              className="flex items-center gap-2 rounded-xl bg-indigo-600 px-4 py-2 text-xs font-semibold text-white shadow-md shadow-indigo-600/30 hover:bg-indigo-500 disabled:opacity-50 transition-colors cursor-pointer"
            >
              {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Register Project
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
