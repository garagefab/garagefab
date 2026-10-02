import { useState, useEffect, useRef } from 'react';
import { X, Terminal, Loader2, Copy, Check, Search, ArrowDown } from 'lucide-react';
import { StepRun } from '../types/api';
import { apiFetchText } from '../lib/api';

interface LogViewerModalProps {
  isOpen: boolean;
  jobId: number;
  step: StepRun | null;
  onClose: () => void;
}

export function LogViewerModal({ isOpen, jobId, step, onClose }: LogViewerModalProps) {
  const [lines, setLines] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState('');
  const [autoScroll, setAutoScroll] = useState(true);
  const [copied, setCopied] = useState(false);

  const logEndRef = useRef<HTMLDivElement>(null);

  // Keyboard navigation: Escape key closes modal (NFR-8)
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  // Lazy loading: fetch/stream logs ONLY when modal is opened (LOG-5, UI-7)
  useEffect(() => {
    if (!isOpen || !step) {
      setLines([]);
      setLoading(false);
      return;
    }

    setLoading(true);
    setError(null);
    setLines([]);

    const logUrl = `/api/jobs/${jobId}/steps/${step.id}/log`;

    if (step.status === 'running') {
      // 1. Live SSE streaming for running steps (UI-7)
      const es = new EventSource(logUrl);

      es.addEventListener('log', (e: MessageEvent) => {
        try {
          const payload = JSON.parse(e.data);
          if (payload.text !== undefined) {
            setLines((prev) => [...prev, payload.text]);
          }
        } catch {
          // ignore
        }
      });

      es.addEventListener('end', () => {
        es.close();
      });

      es.onerror = () => {
        es.close();
      };

      setLoading(false);

      return () => {
        es.close();
      };
    } else {
      // 2. Fetch stored log file for completed steps (UI-7, LOG-5)
      apiFetchText(logUrl)
        .then((raw) => {
          const splitLines = raw ? raw.split('\n') : [];
          setLines(splitLines);
        })
        .catch((err) => {
          setError(err?.message || 'Failed to load stored logs');
        })
        .finally(() => {
          setLoading(false);
        });
    }
  }, [isOpen, jobId, step]);

  // Auto-scroll to bottom when new lines arrive if autoScroll is enabled
  useEffect(() => {
    if (autoScroll && logEndRef.current) {
      logEndRef.current.scrollIntoView({ behavior: 'smooth' });
    }
  }, [lines, autoScroll]);

  if (!isOpen || !step) return null;

  const handleCopy = () => {
    navigator.clipboard.writeText(lines.join('\n'));
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const filteredLines = filter
    ? lines.filter((l) => l.toLowerCase().includes(filter.toLowerCase()))
    : lines;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="log-viewer-title"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/85 backdrop-blur-sm"
    >
      <div className="w-full max-w-4xl h-[85vh] flex flex-col rounded-2xl border border-slate-800 bg-slate-950 shadow-2xl overflow-hidden">
        {/* Modal Header */}
        <div className="flex items-center justify-between px-5 py-3.5 bg-slate-900 border-b border-slate-800 shrink-0">
          <div className="flex items-center gap-2.5">
            <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
              <Terminal className="h-4 w-4" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h2 id="log-viewer-title" className="text-sm font-semibold text-white">
                  Step Logs: {step.stage}
                </h2>
                <span className="rounded px-1.5 py-0.5 text-[10px] font-mono bg-slate-800 text-slate-300 border border-slate-700">
                  Attempt {step.attempt}
                </span>
                {step.status === 'running' && (
                  <span className="inline-flex items-center gap-1 rounded-full bg-blue-500/10 text-blue-400 border border-blue-500/20 px-2 py-0.5 text-[10px] font-medium">
                    <Loader2 className="h-2.5 w-2.5 animate-spin" />
                    Live Streaming
                  </span>
                )}
              </div>
              <p className="text-[11px] text-slate-400 font-mono mt-0.5 truncate max-w-lg">
                {step.log_path || 'Standard execution output'}
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleCopy}
              className="inline-flex items-center gap-1.5 rounded-lg border border-slate-700 bg-slate-800 px-2.5 py-1 text-xs font-medium text-slate-300 hover:text-white transition-colors cursor-pointer"
            >
              {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
              <span>{copied ? 'Copied' : 'Copy'}</span>
            </button>

            <button
              type="button"
              onClick={onClose}
              aria-label="Close log viewer"
              className="rounded-lg p-1.5 text-slate-400 hover:text-white hover:bg-slate-800 transition-colors cursor-pointer"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </div>

        {/* Toolbar: Search Filter & Auto-scroll Toggle */}
        <div className="flex items-center justify-between px-5 py-2.5 bg-slate-900/60 border-b border-slate-800/80 shrink-0 text-xs">
          <div className="flex items-center gap-2 flex-1 max-w-sm">
            <Search className="h-3.5 w-3.5 text-slate-500" />
            <input
              type="text"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Search / filter output..."
              className="w-full bg-transparent text-white placeholder-slate-500 focus:outline-none text-xs font-mono"
            />
            {filter && (
              <button
                type="button"
                onClick={() => setFilter('')}
                className="text-slate-400 hover:text-white"
              >
                Clear
              </button>
            )}
          </div>

          <button
            type="button"
            onClick={() => setAutoScroll(!autoScroll)}
            className={`inline-flex items-center gap-1.5 px-2 py-1 rounded-md text-xs font-medium transition-colors cursor-pointer ${
              autoScroll ? 'bg-indigo-500/20 text-indigo-300' : 'text-slate-400 hover:text-white'
            }`}
          >
            <ArrowDown className="h-3 w-3" />
            <span>Follow output</span>
          </button>
        </div>

        {/* Terminal Output Area */}
        <div className="flex-1 overflow-y-auto p-4 font-mono text-xs leading-relaxed bg-black/90 text-slate-200">
          {loading ? (
            <div className="flex h-full items-center justify-center gap-2 text-slate-400">
              <Loader2 className="h-4 w-4 animate-spin text-indigo-400" />
              <span>Fetching logs...</span>
            </div>
          ) : error ? (
            <div className="text-rose-400 p-4">{error}</div>
          ) : filteredLines.length === 0 ? (
            <div className="text-slate-500 p-4">No output recorded for this step.</div>
          ) : (
            filteredLines.map((line, idx) => (
              <div key={idx} className="flex hover:bg-slate-900/40">
                <span className="select-none text-slate-600 w-12 shrink-0 text-right pr-4">
                  {idx + 1}
                </span>
                <span className="whitespace-pre-wrap break-all flex-1">{line}</span>
              </div>
            ))
          )}
          <div ref={logEndRef} />
        </div>
      </div>
    </div>
  );
}
