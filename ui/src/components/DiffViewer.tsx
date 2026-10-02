import { useState, useMemo } from 'react';
import { ChevronDown, FileDiff, Check } from 'lucide-react';

interface DiffViewerProps {
  diff: string;
  limitLines?: number;
}

export function DiffViewer({ diff, limitLines = 500 }: DiffViewerProps) {
  const [expanded, setExpanded] = useState(false);
  const [copied, setCopied] = useState(false);

  const lines = useMemo(() => diff.split('\n'), [diff]);
  const isTruncated = lines.length > limitLines && !expanded;
  const displayedLines = isTruncated ? lines.slice(0, limitLines) : lines;

  const handleCopy = () => {
    navigator.clipboard.writeText(diff);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  if (!diff.trim()) {
    return (
      <div className="rounded-2xl border border-slate-800 bg-slate-900/50 p-6 text-center text-xs text-slate-400">
        Empty diff (no modifications from merge base).
      </div>
    );
  }

  return (
    <div className="rounded-2xl border border-slate-800 bg-slate-950 shadow-lg overflow-hidden">
      {/* Diff Header */}
      <div className="flex items-center justify-between px-4 py-3 bg-slate-900 border-b border-slate-800">
        <div className="flex items-center gap-2 text-xs font-semibold text-white">
          <FileDiff className="h-4 w-4 text-indigo-400" />
          <span>Unified Git Diff</span>
          <span className="font-mono text-slate-400 font-normal">({lines.length} lines)</span>
        </div>

        <button
          type="button"
          onClick={handleCopy}
          className="rounded-lg border border-slate-700 bg-slate-800 px-2.5 py-1 text-xs font-medium text-slate-300 hover:text-white transition-colors cursor-pointer"
        >
          {copied ? <Check className="h-3 w-3 text-emerald-400 inline mr-1" /> : null}
          {copied ? 'Copied!' : 'Copy Diff'}
        </button>
      </div>

      {/* Diff Code Display */}
      <div className="overflow-x-auto p-4 font-mono text-xs leading-relaxed max-h-[550px] overflow-y-auto">
        {displayedLines.map((line, idx) => {
          let style = 'text-slate-300';
          let bg = '';

          if (line.startsWith('+++') || line.startsWith('---')) {
            style = 'text-indigo-400 font-bold';
            bg = 'bg-indigo-500/5';
          } else if (line.startsWith('+')) {
            style = 'text-emerald-300';
            bg = 'bg-emerald-500/10';
          } else if (line.startsWith('-')) {
            style = 'text-rose-300';
            bg = 'bg-rose-500/10';
          } else if (line.startsWith('@@')) {
            style = 'text-amber-400 font-semibold';
            bg = 'bg-amber-500/5';
          } else if (line.startsWith('diff --git')) {
            style = 'text-white font-bold pt-2 border-t border-slate-800/80 mt-2 first:mt-0 first:pt-0 first:border-0';
          }

          return (
            <div key={idx} className={`px-2 py-0.5 rounded-sm whitespace-pre font-mono ${style} ${bg}`}>
              {line || ' '}
            </div>
          );
        })}

        {/* Truncation Notice & Show More Button (UI-9) */}
        {isTruncated && (
          <div className="mt-4 p-4 rounded-xl border border-slate-800 bg-slate-900/90 text-center space-y-2">
            <p className="text-xs text-slate-400">
              Diff truncated at {limitLines} lines ({lines.length - limitLines} remaining lines hidden to preserve responsiveness).
            </p>
            <button
              type="button"
              onClick={() => setExpanded(true)}
              className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3.5 py-1.5 text-xs font-semibold text-white shadow hover:bg-indigo-500 transition-colors cursor-pointer"
            >
              <span>Show entire diff</span>
              <ChevronDown className="h-3.5 w-3.5" />
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
