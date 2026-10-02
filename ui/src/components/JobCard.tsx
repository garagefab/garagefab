import { Loader2, CheckCircle2, AlertCircle, HelpCircle, Eye, ShieldAlert, AlertTriangle, Clock } from 'lucide-react';
import { Job, JobStatus } from '../types/api';
import { Link } from '../lib/router';

interface JobCardProps {
  job: Job;
}

function getStatusIndicator(status: JobStatus) {
  switch (status) {
    case 'running':
      return {
        icon: <Loader2 className="h-3 w-3 animate-spin text-blue-400" />,
        text: 'Running',
        style: 'text-blue-300 bg-blue-500/10 border-blue-500/20',
      };
    case 'needs_clarification':
      return {
        icon: <HelpCircle className="h-3 w-3 text-amber-400" />,
        text: 'Needs Clarification',
        style: 'text-amber-300 bg-amber-500/10 border-amber-500/20',
      };
    case 'spec_review':
      return {
        icon: <Eye className="h-3 w-3 text-purple-400" />,
        text: 'Spec Review',
        style: 'text-purple-300 bg-purple-500/10 border-purple-500/20',
      };
    case 'awaiting_approval':
      return {
        icon: <ShieldAlert className="h-3 w-3 text-indigo-400" />,
        text: 'Awaiting Gate',
        style: 'text-indigo-300 bg-indigo-500/10 border-indigo-500/20',
      };
    case 'done':
      return {
        icon: <CheckCircle2 className="h-3 w-3 text-emerald-400" />,
        text: 'Done',
        style: 'text-emerald-300 bg-emerald-500/10 border-emerald-500/20',
      };
    case 'failed':
      return {
        icon: <AlertCircle className="h-3 w-3 text-rose-400" />,
        text: 'Failed',
        style: 'text-rose-300 bg-rose-500/10 border-rose-500/20',
      };
    case 'interrupted':
      return {
        icon: <AlertTriangle className="h-3 w-3 text-orange-400" />,
        text: 'Interrupted',
        style: 'text-orange-300 bg-orange-500/10 border-orange-500/20',
      };
    case 'cancelled':
      return {
        icon: <Clock className="h-3 w-3 text-slate-400" />,
        text: 'Cancelled',
        style: 'text-slate-400 bg-slate-800/80 border-slate-700',
      };
    case 'queued':
    default:
      return {
        icon: <Clock className="h-3 w-3 text-slate-400" />,
        text: 'Queued',
        style: 'text-slate-300 bg-slate-800/80 border-slate-700',
      };
  }
}

function getWorkTypeColor(type: string) {
  switch (type) {
    case 'feature':
      return 'bg-indigo-500/10 text-indigo-300 border-indigo-500/20';
    case 'bug_fix':
      return 'bg-rose-500/10 text-rose-300 border-rose-500/20';
    case 'refactor':
      return 'bg-teal-500/10 text-teal-300 border-teal-500/20';
    case 'docs':
      return 'bg-amber-500/10 text-amber-300 border-amber-500/20';
    default:
      return 'bg-slate-800 text-slate-300 border-slate-700';
  }
}

export function JobCard({ job }: JobCardProps) {
  const status = getStatusIndicator(job.status);
  const workTypeStyle = getWorkTypeColor(job.work_type);

  return (
    <Link
      href={`/jobs/${job.id}`}
      className="block w-full rounded-xl border border-slate-800 bg-slate-900/90 p-4 shadow-sm hover:border-slate-700 hover:bg-slate-850 hover:shadow-md transition-all group select-none text-left"
    >
      <div className="flex items-center justify-between gap-2 mb-2">
        <span className="font-mono text-xs font-semibold text-slate-400 group-hover:text-indigo-400 transition-colors">
          #{job.id}
        </span>
        <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium border ${workTypeStyle}`}>
          {job.work_type}
        </span>
      </div>

      <h4 className="text-sm font-medium text-white line-clamp-2 leading-snug group-hover:text-indigo-200 transition-colors">
        {job.title}
      </h4>

      <div className="mt-3 flex items-center justify-between pt-2 border-t border-slate-800/60">
        <div className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${status.style}`}>
          {status.icon}
          <span>{status.text}</span>
        </div>

        <span className="text-[11px] text-slate-400 font-mono truncate max-w-[80px]">
          {job.project_name || `P:${job.project_id}`}
        </span>
      </div>
    </Link>
  );
}
