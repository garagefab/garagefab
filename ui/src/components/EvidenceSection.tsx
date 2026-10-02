import { useState } from 'react';
import { ShieldCheck, ShieldAlert, AlertTriangle, CheckCircle2, XCircle, MinusCircle, FileDiff } from 'lucide-react';
import { EvidenceSummary } from '../types/api';

interface EvidenceSectionProps {
  evidence: EvidenceSummary;
  onViewDiff?: () => void;
}

function getCheckBadge(status: string) {
  switch (status) {
    case 'pass':
      return {
        icon: <CheckCircle2 className="h-3.5 w-3.5 text-emerald-400" />,
        text: 'Pass',
        style: 'bg-emerald-500/10 text-emerald-300 border-emerald-500/20',
      };
    case 'fail':
      return {
        icon: <XCircle className="h-3.5 w-3.5 text-rose-400" />,
        text: 'Fail',
        style: 'bg-rose-500/10 text-rose-300 border-rose-500/20',
      };
    default:
      return {
        icon: <MinusCircle className="h-3.5 w-3.5 text-slate-500" />,
        text: 'Skipped',
        style: 'bg-slate-800 text-slate-400 border-slate-700',
      };
  }
}

export function EvidenceSection({ evidence, onViewDiff }: EvidenceSectionProps) {
  const [warningsOpen, setWarningsOpen] = useState(false);

  const buildBadge = getCheckBadge(evidence.build_status);
  const testBadge = getCheckBadge(evidence.test_status);
  const lintBadge = getCheckBadge(evidence.lint_status);

  const isApproved = evidence.review_decision === 'approve';
  const warningsCount = evidence.warnings_count || 0;

  return (
    <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5 shadow-lg space-y-5">
      {/* Header & Verdict Banner */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-4 border-b border-slate-800">
        <div className="flex items-center gap-3">
          <div
            className={`flex h-10 w-10 items-center justify-center rounded-xl border ${
              isApproved
                ? 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20'
                : 'bg-amber-500/10 text-amber-400 border-amber-500/20'
            }`}
          >
            {isApproved ? <ShieldCheck className="h-5 w-5" /> : <ShieldAlert className="h-5 w-5" />}
          </div>
          <div>
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold text-white">Chain of Evidence</h3>
              <span
                className={`rounded-full px-2.5 py-0.5 text-xs font-semibold uppercase tracking-wider ${
                  isApproved ? 'bg-emerald-500/20 text-emerald-300' : 'bg-amber-500/20 text-amber-300'
                }`}
              >
                {evidence.review_decision || 'Pending'}
              </span>
            </div>
            <p className="text-xs text-slate-400 font-mono mt-0.5">HEAD: {evidence.head_sha || 'N/A'}</p>
          </div>
        </div>

        {/* Review Warnings Badge (UI-8) */}
        {warningsCount > 0 && (
          <button
            type="button"
            onClick={() => setWarningsOpen(!warningsOpen)}
            className="inline-flex items-center gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs font-semibold text-amber-300 hover:bg-amber-500/20 transition-colors cursor-pointer"
          >
            <AlertTriangle className="h-4 w-4 text-amber-400" />
            <span>⚠️ {warningsCount} Review {warningsCount === 1 ? 'Warning' : 'Warnings'}</span>
          </button>
        )}
      </div>

      {/* Warnings Panel (collapsible per UI-8) */}
      {warningsOpen && evidence.warnings && evidence.warnings.length > 0 && (
        <div
          role="region"
          aria-label="Review warnings list"
          className="rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 space-y-2 text-xs text-amber-200"
        >
          <div className="font-semibold text-amber-100 flex items-center gap-1.5">
            <AlertTriangle className="h-4 w-4" />
            <span>Non-blocking Review Warnings ({warningsCount}):</span>
          </div>
          <ul className="list-disc list-inside space-y-1 pl-1">
            {evidence.warnings.map((w, idx) => (
              <li key={idx} className="text-amber-200/90">
                {w}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* Verification Checks Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <div className="rounded-xl border border-slate-800 bg-slate-950 p-3 flex items-center justify-between">
          <span className="text-xs font-medium text-slate-300">Build Command</span>
          <div className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium ${buildBadge.style}`}>
            {buildBadge.icon}
            <span>{buildBadge.text}</span>
          </div>
        </div>

        <div className="rounded-xl border border-slate-800 bg-slate-950 p-3 flex items-center justify-between">
          <span className="text-xs font-medium text-slate-300">Test Suite</span>
          <div className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium ${testBadge.style}`}>
            {testBadge.icon}
            <span>{testBadge.text}</span>
          </div>
        </div>

        <div className="rounded-xl border border-slate-800 bg-slate-950 p-3 flex items-center justify-between">
          <span className="text-xs font-medium text-slate-300">Linter</span>
          <div className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs font-medium ${lintBadge.style}`}>
            {lintBadge.icon}
            <span>{lintBadge.text}</span>
          </div>
        </div>
      </div>

      {/* Diff Metrics & Drill-down Button */}
      <div className="flex flex-wrap items-center justify-between gap-3 pt-3 border-t border-slate-800 text-xs">
        <div className="flex items-center gap-4 text-slate-300">
          <span>Files Changed: <strong className="text-white font-mono">{evidence.files_changed}</strong></span>
          <span className="text-emerald-400 font-mono">+{evidence.insertions}</span>
          <span className="text-rose-400 font-mono">-{evidence.deletions}</span>
        </div>

        {onViewDiff && (
          <button
            type="button"
            onClick={onViewDiff}
            className="inline-flex items-center gap-1.5 text-indigo-400 hover:text-indigo-300 transition-colors font-medium cursor-pointer"
          >
            <FileDiff className="h-3.5 w-3.5" />
            <span>Inspect Diff</span>
          </button>
        )}
      </div>
    </div>
  );
}
