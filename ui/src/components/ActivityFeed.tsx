import { Activity, Clock } from 'lucide-react';
import { SystemEvent } from '../types/api';
import { Link } from '../lib/router';

interface ActivityFeedProps {
  events: SystemEvent[];
}

export function ActivityFeed({ events }: ActivityFeedProps) {
  if (events.length === 0) {
    return (
      <div className="rounded-2xl border border-slate-800 bg-slate-900/50 p-6 text-center text-sm text-slate-400">
        No recent activity logged yet.
      </div>
    );
  }

  return (
    <div className="rounded-2xl border border-slate-800 bg-slate-900/70 p-5 shadow-lg">
      <div className="flex items-center gap-2 mb-4">
        <Activity className="h-4 w-4 text-indigo-400" />
        <h2 className="text-base font-semibold text-white">Recent Activity</h2>
      </div>

      <div className="space-y-3">
        {events.map((ev) => (
          <div
            key={ev.id}
            className="flex items-start justify-between gap-3 text-xs border-b border-slate-800/60 pb-2.5 last:border-b-0 last:pb-0"
          >
            <div className="min-w-0">
              <div className="flex items-center gap-2">
                <span className="font-mono text-slate-400">#{ev.job_id}</span>
                {ev.job_title && (
                  <Link
                    href={`/jobs/${ev.job_id}`}
                    className="font-medium text-slate-200 hover:text-indigo-300 truncate transition-colors"
                  >
                    {ev.job_title}
                  </Link>
                )}
              </div>
              <p className="text-slate-400 mt-0.5 font-mono text-[11px]">{ev.type}</p>
            </div>

            <div className="flex items-center gap-1 text-slate-400 shrink-0">
              <Clock className="h-3 w-3" />
              <span>{new Date(ev.created_at).toLocaleTimeString()}</span>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
