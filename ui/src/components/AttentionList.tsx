import { AlertCircle, HelpCircle, Eye, ShieldAlert, AlertTriangle, ArrowRight } from 'lucide-react';
import { AttentionItem, JobStatus } from '../types/api';
import { Link } from '../lib/router';

interface AttentionListProps {
  items: AttentionItem[];
}

function getStatusBadge(status: JobStatus) {
  switch (status) {
    case 'needs_clarification':
      return {
        icon: <HelpCircle className="h-3.5 w-3.5 text-amber-400" />,
        label: 'Needs Clarification',
        bg: 'bg-amber-500/10 text-amber-300 border-amber-500/20',
      };
    case 'spec_review':
      return {
        icon: <Eye className="h-3.5 w-3.5 text-purple-400" />,
        label: 'Spec Review',
        bg: 'bg-purple-500/10 text-purple-300 border-purple-500/20',
      };
    case 'awaiting_approval':
      return {
        icon: <ShieldAlert className="h-3.5 w-3.5 text-indigo-400" />,
        label: 'Awaiting Approval',
        bg: 'bg-indigo-500/10 text-indigo-300 border-indigo-500/20',
      };
    case 'failed':
      return {
        icon: <AlertCircle className="h-3.5 w-3.5 text-rose-400" />,
        label: 'Failed',
        bg: 'bg-rose-500/10 text-rose-300 border-rose-500/20',
      };
    case 'interrupted':
      return {
        icon: <AlertTriangle className="h-3.5 w-3.5 text-orange-400" />,
        label: 'Interrupted',
        bg: 'bg-orange-500/10 text-orange-300 border-orange-500/20',
      };
    default:
      return {
        icon: <AlertCircle className="h-3.5 w-3.5 text-slate-400" />,
        label: status,
        bg: 'bg-slate-800 text-slate-300 border-slate-700',
      };
  }
}

export function AttentionList({ items }: AttentionListProps) {
  if (items.length === 0) {
    return (
      <div className="rounded-2xl border border-slate-800 bg-slate-900/50 p-6 text-center text-sm text-slate-400">
        No items requiring immediate attention. Factory running smoothly.
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-2xl border border-slate-800 bg-slate-900/70 shadow-lg">
      <div className="px-5 py-4 border-b border-slate-800 flex items-center justify-between">
        <div className="flex items-center gap-2">
          <span className="flex h-2 w-2 rounded-full bg-amber-400 animate-pulse" />
          <h2 className="text-base font-semibold text-white">Attention Needed</h2>
          <span className="rounded-full bg-amber-500/10 border border-amber-500/20 px-2 py-0.5 text-xs font-medium text-amber-300">
            {items.length}
          </span>
        </div>
      </div>

      <div className="divide-y divide-slate-800/80">
        {items.map((item) => {
          const badge = getStatusBadge(item.status);
          return (
            <Link
              key={item.id}
              href={`/jobs/${item.id}`}
              className="flex items-center justify-between px-5 py-4 hover:bg-slate-800/40 transition-colors group"
            >
              <div className="min-w-0 pr-4">
                <div className="flex items-center gap-2.5 flex-wrap">
                  <span className="font-mono text-xs text-slate-400">#{item.id}</span>
                  <span className="text-sm font-medium text-white truncate group-hover:text-indigo-300 transition-colors">
                    {item.title}
                  </span>
                  <span className="rounded px-1.5 py-0.5 text-[11px] font-mono bg-slate-800 text-slate-400 border border-slate-700/60">
                    {item.project_name}
                  </span>
                  <span className="rounded px-1.5 py-0.5 text-[11px] font-medium bg-slate-800/80 text-slate-300">
                    {item.work_type}
                  </span>
                </div>
                <div className="mt-1 flex items-center gap-2 text-xs text-slate-400">
                  <span>Stage: {item.stage}</span>
                  <span>•</span>
                  <span>{new Date(item.updated_at).toLocaleString()}</span>
                </div>
              </div>

              <div className="flex items-center gap-3 shrink-0">
                <div
                  className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium ${badge.bg}`}
                >
                  {badge.icon}
                  <span>{badge.label}</span>
                </div>
                <ArrowRight className="h-4 w-4 text-slate-500 group-hover:text-white group-hover:translate-x-0.5 transition-all" />
              </div>
            </Link>
          );
        })}
      </div>
    </div>
  );
}
