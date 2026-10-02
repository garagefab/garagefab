import { useEffect, useState, useCallback } from 'react';
import {
  Layers,
  PlayCircle,
  HelpCircle,
  CheckCircle2,
  AlertTriangle,
  FolderGit2,
  PlusCircle,
  Kanban,
  RefreshCw,
} from 'lucide-react';
import { OverviewData } from '../types/api';
import { apiFetch } from '../lib/api';
import { AttentionList } from '../components/AttentionList';
import { ActivityFeed } from '../components/ActivityFeed';
import { Link } from '../lib/router';
import { useSSE } from '../hooks/useSSE';

interface OverviewPageProps {
  onOpenNewJob: () => void;
  onOpenAddProject: () => void;
}

export function OverviewPage({ onOpenNewJob, onOpenAddProject }: OverviewPageProps) {
  const [data, setData] = useState<OverviewData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const loadOverview = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const res = await apiFetch<OverviewData>('/api/overview');
      setData(res);
    } catch (err: any) {
      setError(err?.message || 'Failed to load dashboard overview');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadOverview();
  }, [loadOverview]);

  // Live SSE listener: auto-update overview metrics on pipeline events (UI-5)
  useSSE(loadOverview);

  if (loading && !data) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center">
        <div className="flex items-center gap-3 text-slate-400">
          <RefreshCw className="h-5 w-5 animate-spin text-indigo-400" />
          <span>Loading factory metrics...</span>
        </div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="p-8">
        <div
          role="alert"
          className="rounded-2xl border border-rose-500/30 bg-rose-500/10 p-6 text-rose-300 flex items-center justify-between"
        >
          <div className="flex items-center gap-3">
            <AlertTriangle className="h-6 w-6 text-rose-400" />
            <div>
              <h3 className="font-semibold text-rose-200">Unable to load metrics</h3>
              <p className="text-sm mt-0.5">{error}</p>
            </div>
          </div>
          <button
            onClick={loadOverview}
            className="rounded-xl bg-rose-500/20 border border-rose-500/30 px-4 py-2 text-xs font-semibold text-rose-200 hover:bg-rose-500/30 transition-colors"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  const counts = data?.job_counts || {
    queued: 0,
    running: 0,
    needs_clarification: 0,
    spec_review: 0,
    awaiting_approval: 0,
    done: 0,
    failed: 0,
    interrupted: 0,
    cancelled: 0,
  };

  const totalActive =
    counts.queued +
    counts.running +
    counts.needs_clarification +
    counts.spec_review +
    counts.awaiting_approval;

  const attentionTotal = (data?.attention_list || []).length;

  return (
    <div className="space-y-8 p-6 lg:p-8 max-w-7xl mx-auto">
      {/* Top Banner & Quick Actions */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 border-b border-slate-800 pb-6">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-white sm:text-3xl">Factory Overview</h1>
          <p className="mt-1 text-sm text-slate-400">
            Real-time status across projects, active pipelines, and human approval gates.
          </p>
        </div>

        <div className="flex items-center gap-3">
          <Link
            href="/board"
            className="inline-flex items-center gap-2 rounded-xl border border-slate-700 bg-slate-800/80 px-3.5 py-2 text-xs font-medium text-slate-200 hover:bg-slate-700/80 transition-colors shadow-sm"
          >
            <Kanban className="h-4 w-4 text-indigo-400" />
            Project Board
          </Link>
          <button
            onClick={onOpenAddProject}
            className="inline-flex items-center gap-2 rounded-xl border border-slate-700 bg-slate-800/80 px-3.5 py-2 text-xs font-medium text-slate-200 hover:bg-slate-700/80 transition-colors shadow-sm cursor-pointer"
          >
            <FolderGit2 className="h-4 w-4 text-indigo-400" />
            Add Project
          </button>
          <button
            onClick={onOpenNewJob}
            className="inline-flex items-center gap-2 rounded-xl bg-indigo-600 px-3.5 py-2 text-xs font-semibold text-white shadow-lg shadow-indigo-600/30 hover:bg-indigo-500 transition-colors cursor-pointer"
          >
            <PlusCircle className="h-4 w-4" />
            New Job
          </button>
        </div>
      </div>

      {/* Metric Cards (UI-1) */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* Active Jobs */}
        <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5 shadow-sm">
          <div className="flex items-center justify-between text-slate-400">
            <span className="text-xs font-medium">In Pipeline</span>
            <Layers className="h-4 w-4 text-indigo-400" />
          </div>
          <div className="mt-3 flex items-baseline gap-2">
            <span className="text-3xl font-bold text-white">{totalActive}</span>
            <span className="text-xs text-slate-400 font-mono">active jobs</span>
          </div>
        </div>

        {/* Running Jobs */}
        <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5 shadow-sm">
          <div className="flex items-center justify-between text-slate-400">
            <span className="text-xs font-medium">Currently Running</span>
            <PlayCircle className="h-4 w-4 text-blue-400" />
          </div>
          <div className="mt-3 flex items-baseline gap-2">
            <span className="text-3xl font-bold text-blue-400">{counts.running}</span>
            <span className="text-xs text-slate-400 font-mono">parallel runners</span>
          </div>
        </div>

        {/* Attention Items */}
        <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5 shadow-sm">
          <div className="flex items-center justify-between text-slate-400">
            <span className="text-xs font-medium">Requires Attention</span>
            <HelpCircle className="h-4 w-4 text-amber-400" />
          </div>
          <div className="mt-3 flex items-baseline gap-2">
            <span className="text-3xl font-bold text-amber-400">{attentionTotal}</span>
            <span className="text-xs text-slate-400 font-mono">gates & clarif.</span>
          </div>
        </div>

        {/* Completed Jobs */}
        <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5 shadow-sm">
          <div className="flex items-center justify-between text-slate-400">
            <span className="text-xs font-medium">Delivered / Done</span>
            <CheckCircle2 className="h-4 w-4 text-emerald-400" />
          </div>
          <div className="mt-3 flex items-baseline gap-2">
            <span className="text-3xl font-bold text-emerald-400">{counts.done}</span>
            <span className="text-xs text-slate-400 font-mono">completed jobs</span>
          </div>
        </div>
      </div>

      {/* Main Grid: Attention List + Activity Feed */}
      <div className="grid grid-cols-1 lg:grid-cols-3 gap-8">
        <div className="lg:col-span-2 space-y-6">
          <AttentionList items={data?.attention_list || []} />

          {/* Status Breakdown Pills */}
          <div className="rounded-2xl border border-slate-800 bg-slate-900/40 p-5">
            <h3 className="text-xs font-medium text-slate-400 uppercase tracking-wider mb-3">Status Breakdown</h3>
            <div className="flex flex-wrap gap-2 text-xs">
              <span className="rounded-full bg-slate-800 px-3 py-1 font-mono text-slate-300 border border-slate-700/60">
                Queued: {counts.queued}
              </span>
              <span className="rounded-full bg-blue-500/10 px-3 py-1 font-mono text-blue-300 border border-blue-500/20">
                Running: {counts.running}
              </span>
              <span className="rounded-full bg-amber-500/10 px-3 py-1 font-mono text-amber-300 border border-amber-500/20">
                Needs Clarification: {counts.needs_clarification}
              </span>
              <span className="rounded-full bg-purple-500/10 px-3 py-1 font-mono text-purple-300 border border-purple-500/20">
                Spec Review: {counts.spec_review}
              </span>
              <span className="rounded-full bg-indigo-500/10 px-3 py-1 font-mono text-indigo-300 border border-indigo-500/20">
                Awaiting Approval: {counts.awaiting_approval}
              </span>
              <span className="rounded-full bg-rose-500/10 px-3 py-1 font-mono text-rose-300 border border-rose-500/20">
                Failed: {counts.failed}
              </span>
              <span className="rounded-full bg-orange-500/10 px-3 py-1 font-mono text-orange-300 border border-orange-500/20">
                Interrupted: {counts.interrupted}
              </span>
              <span className="rounded-full bg-slate-800 px-3 py-1 font-mono text-slate-400 border border-slate-700/60">
                Cancelled: {counts.cancelled}
              </span>
            </div>
          </div>
        </div>

        {/* Sidebar: Activity Feed & System Health */}
        <div className="space-y-6">
          <ActivityFeed events={data?.recent_activity || []} />

          {/* Orphan worktrees / intake warning (UI-1) */}
          {((data?.orphan_worktrees && data.orphan_worktrees.length > 0) ||
            (data?.intake_errors && data.intake_errors.length > 0)) && (
            <div className="rounded-2xl border border-amber-500/30 bg-amber-500/10 p-5 text-xs text-amber-300 space-y-2">
              <div className="flex items-center gap-2 font-semibold text-amber-200">
                <AlertTriangle className="h-4 w-4" />
                <span>System Warnings</span>
              </div>
              {data.orphan_worktrees.length > 0 && (
                <p>Orphan worktrees detected: {data.orphan_worktrees.join(', ')}</p>
              )}
              {data.intake_errors.length > 0 && (
                <p>Intake errors: {data.intake_errors.join(', ')}</p>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
