import { useState } from 'react';
import { CheckCircle2, XCircle, RotateCcw, StopCircle, Copy, Check, Loader2 } from 'lucide-react';
import { Job, JobStatus } from '../types/api';
import { RejectModal } from './RejectModal';

interface JobActionsProps {
  job: Job;
  onApprove: () => Promise<void>;
  onReject: (note: string) => Promise<void>;
  onRetry: () => Promise<void>;
  onCancel: () => Promise<void>;
}

export function JobActions({ job, onApprove, onReject, onRetry, onCancel }: JobActionsProps) {
  const [copied, setCopied] = useState(false);
  const [rejectModalOpen, setRejectModalOpen] = useState(false);
  const [actionLoading, setActionLoading] = useState<string | null>(null);

  const status: JobStatus = job.status;

  // Strict state matrix per UI-4
  const canApprove = status === 'spec_review' || status === 'awaiting_approval';
  const canReject = status === 'awaiting_approval';
  const canRetry = status === 'failed' || status === 'interrupted';
  const canCancel =
    status === 'queued' ||
    status === 'running' ||
    status === 'needs_clarification' ||
    status === 'spec_review' ||
    status === 'awaiting_approval';

  const handoffCmd = job.handoff_command;

  const handleCopyHandoff = () => {
    if (!handoffCmd) return;
    navigator.clipboard.writeText(handoffCmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const handleAction = async (name: string, fn: () => Promise<void>) => {
    setActionLoading(name);
    try {
      await fn();
    } finally {
      setActionLoading(null);
    }
  };

  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-3 p-4 rounded-2xl border border-slate-800 bg-slate-900/80 shadow-md">
        {/* Left: Lifecycle Gate Action Buttons (UI-4) */}
        <div className="flex flex-wrap items-center gap-2">
          {/* Approve Button */}
          <button
            onClick={() => handleAction('approve', onApprove)}
            disabled={!canApprove || actionLoading !== null}
            className={`inline-flex items-center gap-1.5 rounded-xl px-3.5 py-2 text-xs font-semibold shadow-sm transition-all cursor-pointer ${
              canApprove
                ? 'bg-emerald-600 text-white shadow-emerald-600/20 hover:bg-emerald-500'
                : 'bg-slate-800/60 text-slate-500 border border-slate-800 cursor-not-allowed opacity-50'
            }`}
          >
            {actionLoading === 'approve' ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <CheckCircle2 className="h-3.5 w-3.5" />
            )}
            Approve {status === 'spec_review' ? 'Spec' : 'Gate'}
          </button>

          {/* Reject Button */}
          <button
            onClick={() => setRejectModalOpen(true)}
            disabled={!canReject || actionLoading !== null}
            className={`inline-flex items-center gap-1.5 rounded-xl px-3.5 py-2 text-xs font-semibold shadow-sm transition-all cursor-pointer ${
              canReject
                ? 'bg-rose-600 text-white shadow-rose-600/20 hover:bg-rose-500'
                : 'bg-slate-800/60 text-slate-500 border border-slate-800 cursor-not-allowed opacity-50'
            }`}
          >
            <XCircle className="h-3.5 w-3.5" />
            Reject...
          </button>

          {/* Retry Button */}
          <button
            onClick={() => handleAction('retry', onRetry)}
            disabled={!canRetry || actionLoading !== null}
            className={`inline-flex items-center gap-1.5 rounded-xl px-3.5 py-2 text-xs font-semibold shadow-sm transition-all cursor-pointer ${
              canRetry
                ? 'bg-indigo-600 text-white shadow-indigo-600/20 hover:bg-indigo-500'
                : 'bg-slate-800/60 text-slate-500 border border-slate-800 cursor-not-allowed opacity-50'
            }`}
          >
            {actionLoading === 'retry' ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <RotateCcw className="h-3.5 w-3.5" />
            )}
            Retry Step
          </button>

          {/* Cancel Button */}
          <button
            onClick={() => handleAction('cancel', onCancel)}
            disabled={!canCancel || actionLoading !== null}
            className={`inline-flex items-center gap-1.5 rounded-xl px-3.5 py-2 text-xs font-semibold border transition-all cursor-pointer ${
              canCancel
                ? 'border-slate-700 bg-slate-800 text-slate-300 hover:bg-slate-700 hover:text-white'
                : 'border-slate-800 bg-slate-800/40 text-slate-600 cursor-not-allowed opacity-50'
            }`}
          >
            {actionLoading === 'cancel' ? (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            ) : (
              <StopCircle className="h-3.5 w-3.5" />
            )}
            Cancel
          </button>
        </div>

        {/* Right: Handoff Command Button (HND-1) */}
        {handoffCmd && (
          <div className="flex items-center gap-2">
            <div className="hidden sm:flex items-center gap-2 rounded-xl border border-slate-800 bg-slate-950 px-3 py-1.5 font-mono text-xs text-slate-400">
              <span>{handoffCmd}</span>
            </div>
            <button
              onClick={handleCopyHandoff}
              title="Copy handoff CLI command (HND-1)"
              className="inline-flex items-center gap-1.5 rounded-xl border border-slate-700 bg-slate-800 px-3 py-2 text-xs font-medium text-slate-200 hover:bg-slate-700 transition-colors shadow-sm cursor-pointer"
            >
              {copied ? (
                <>
                  <Check className="h-3.5 w-3.5 text-emerald-400" />
                  <span className="text-emerald-400">Copied!</span>
                </>
              ) : (
                <>
                  <Copy className="h-3.5 w-3.5" />
                  <span>Handoff</span>
                </>
              )}
            </button>
          </div>
        )}
      </div>

      {/* Rejection Modal (APR-6) */}
      <RejectModal
        isOpen={rejectModalOpen}
        onClose={() => setRejectModalOpen(false)}
        onConfirmReject={onReject}
      />
    </>
  );
}
