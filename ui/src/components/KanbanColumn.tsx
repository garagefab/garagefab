import { Job } from '../types/api';
import { JobCard } from './JobCard';

interface KanbanColumnProps {
  id: string;
  title: string;
  subtitle: string;
  jobs: Job[];
}

export function KanbanColumn({ title, subtitle, jobs }: KanbanColumnProps) {
  const hasRunning = jobs.some((j) => j.status === 'running');

  return (
    <div className="flex flex-col min-w-[280px] max-w-[320px] w-full shrink-0 rounded-2xl border border-slate-800/90 bg-slate-900/40 p-3.5 shadow-sm">
      {/* Column Header */}
      <div className="flex items-center justify-between pb-3 border-b border-slate-800/80 mb-3 px-1">
        <div>
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold text-white tracking-wide">{title}</h3>
            {hasRunning && (
              <span className="relative flex h-2 w-2" title="Active runner in progress">
                <span className="animate-ping absolute inline-flex h-full w-full rounded-full bg-blue-400 opacity-75" />
                <span className="relative inline-flex rounded-full h-2 w-2 bg-blue-500" />
              </span>
            )}
          </div>
          <p className="text-[11px] text-slate-400 mt-0.5">{subtitle}</p>
        </div>

        <span className="flex h-5 min-w-5 items-center justify-center rounded-full bg-slate-800 px-1.5 font-mono text-xs text-slate-300 border border-slate-700">
          {jobs.length}
        </span>
      </div>

      {/* Cards List (Strictly Read-Only, UI-2) */}
      <div className="flex-1 space-y-3 overflow-y-auto max-h-[calc(100vh-250px)] pr-1">
        {jobs.length === 0 ? (
          <div className="rounded-xl border border-dashed border-slate-800/80 p-6 text-center text-xs text-slate-400 select-none">
            No jobs in stage
          </div>
        ) : (
          jobs.map((job) => <JobCard key={job.id} job={job} />)
        )}
      </div>
    </div>
  );
}
