import { ShieldCheck, ShieldAlert, AlertTriangle, CheckCircle2, XCircle, MinusCircle } from 'lucide-react';
import { ReviewReport } from '../types/api';

interface ReviewReportViewProps {
  report: ReviewReport | null;
  loading: boolean;
  error: string | null;
}

function coverageBadge(status: string) {
  switch (status) {
    case 'met':
      return {
        icon: <CheckCircle2 className="h-3.5 w-3.5 text-emerald-400" />,
        text: 'Met',
        style: 'bg-emerald-500/10 text-emerald-300 border-emerald-500/20',
      };
    case 'partial':
      return {
        icon: <AlertTriangle className="h-3.5 w-3.5 text-amber-400" />,
        text: 'Partial',
        style: 'bg-amber-500/10 text-amber-300 border-amber-500/20',
      };
    case 'not_met':
      return {
        icon: <XCircle className="h-3.5 w-3.5 text-rose-400" />,
        text: 'Not met',
        style: 'bg-rose-500/10 text-rose-300 border-rose-500/20',
      };
    default:
      return {
        icon: <MinusCircle className="h-3.5 w-3.5 text-slate-500" />,
        text: status || 'Unknown',
        style: 'bg-slate-800 text-slate-400 border-slate-700',
      };
  }
}

export function ReviewReportView({ report, loading, error }: ReviewReportViewProps) {
  if (loading) {
    return <div className="p-6 text-center text-xs text-slate-400">Loading review report...</div>;
  }

  if (error) {
    return <div className="p-6 text-center text-xs text-rose-400">{error}</div>;
  }

  if (!report) {
    return (
      <div className="p-6 text-center text-xs text-slate-400">
        Review report not yet generated. It appears after 05_Independent_Review.
      </div>
    );
  }

  const isApproved = report.decision === 'approve';
  const findings = report.findings || [];
  const warnings = report.warnings || [];
  const coverage = report.spec_coverage || [];
  const risks = [
    { label: 'Side Effect', item: report.risk?.side_effect },
    { label: 'Performance', item: report.risk?.performance },
    { label: 'Compatibility', item: report.risk?.backward_compatibility },
  ];

  return (
    <div className="rounded-2xl border border-slate-800 bg-slate-900/60 p-5 shadow-lg space-y-5">
      <div className="flex items-center gap-3 pb-4 border-b border-slate-800">
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
            <h3 className="text-sm font-semibold text-white">Independent Review</h3>
            <span
              className={`rounded-full px-2.5 py-0.5 text-xs font-semibold uppercase tracking-wider ${
                isApproved ? 'bg-emerald-500/20 text-emerald-300' : 'bg-amber-500/20 text-amber-300'
              }`}
            >
              {report.decision === 'request_changes' ? 'Request changes' : report.decision}
            </span>
          </div>
          <p className="text-xs text-slate-400 mt-1 leading-relaxed">{report.summary}</p>
        </div>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        {risks.map(({ label, item }) => (
          <div key={label} className="rounded-xl border border-slate-800 bg-slate-950 p-3">
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-slate-300">{label}</span>
              <span className="text-xs font-mono text-white">{item ? `${item.score}/5` : 'N/A'}</span>
            </div>
            {item && <p className="mt-1.5 text-xs text-slate-400 leading-relaxed">{item.rationale}</p>}
          </div>
        ))}
      </div>

      <div>
        <h4 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">
          Findings ({findings.length})
        </h4>
        {findings.length === 0 ? (
          <p className="text-xs text-slate-500">No findings.</p>
        ) : (
          <ul className="space-y-2">
            {findings.map((f, idx) => (
              <li key={idx} className="rounded-xl border border-slate-800 bg-slate-950 p-3 text-xs">
                <div className="flex items-center gap-2">
                  <span
                    className={`rounded-full px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wider border ${
                      f.severity === 'blocking'
                        ? 'bg-rose-500/10 text-rose-300 border-rose-500/20'
                        : 'bg-slate-800 text-slate-300 border-slate-700'
                    }`}
                  >
                    {f.severity}
                  </span>
                  <span className="font-mono text-slate-300">
                    {f.file}
                    {f.line ? `:${f.line}` : ''}
                  </span>
                </div>
                <p className="mt-1.5 text-slate-400 leading-relaxed">{f.description}</p>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div>
        <h4 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">
          Warnings ({warnings.length})
        </h4>
        {warnings.length === 0 ? (
          <p className="text-xs text-slate-500">No warnings.</p>
        ) : (
          <div
            role="region"
            aria-label="Review warnings list"
            className="rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 space-y-2 text-xs text-amber-200"
          >
            <ul className="list-disc list-inside space-y-1 pl-1">
              {warnings.map((w, idx) => (
                <li key={idx} className="text-amber-200/90">
                  <span className="font-mono">{w.file}</span>
                  {' — '}
                  {w.description}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>

      <div>
        <h4 className="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">
          Spec Coverage ({coverage.length})
        </h4>
        {coverage.length === 0 ? (
          <p className="text-xs text-slate-500">No coverage items.</p>
        ) : (
          <div className="overflow-x-auto rounded-xl border border-slate-800">
            <table className="w-full text-xs">
              <thead>
                <tr className="bg-slate-950 text-left text-slate-400">
                  <th className="px-3 py-2 font-semibold">Criterion</th>
                  <th className="px-3 py-2 font-semibold">Status</th>
                  <th className="px-3 py-2 font-semibold">Note</th>
                </tr>
              </thead>
              <tbody>
                {coverage.map((c, idx) => {
                  const badge = coverageBadge(c.status);
                  return (
                    <tr key={idx} className="border-t border-slate-800 bg-slate-900/40">
                      <td className="px-3 py-2 font-mono text-slate-200 whitespace-nowrap">{c.criterion}</td>
                      <td className="px-3 py-2">
                        <span
                          className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${badge.style}`}
                        >
                          {badge.icon}
                          <span>{badge.text}</span>
                        </span>
                      </td>
                      <td className="px-3 py-2 text-slate-400 leading-relaxed">{c.note}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
