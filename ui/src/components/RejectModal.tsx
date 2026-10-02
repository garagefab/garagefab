import { useState, FormEvent, useEffect } from 'react';
import { X, XCircle, Loader2 } from 'lucide-react';

interface RejectModalProps {
  isOpen: boolean;
  onClose: () => void;
  onConfirmReject: (note: string) => Promise<void>;
}

export function RejectModal({ isOpen, onClose, onConfirmReject }: RejectModalProps) {
  const [note, setNote] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isOpen) {
      setNote('');
      setError(null);
    }
  }, [isOpen]);

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isOpen) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  if (!isOpen) return null;

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!note.trim()) {
      setError('A rejection note is required to explain what needs correction (APR-6)');
      return;
    }

    setLoading(true);
    setError(null);

    try {
      await onConfirmReject(note.trim());
      onClose();
    } catch (err: any) {
      setError(err?.message || 'Failed to reject job');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby="reject-job-title"
      className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-slate-950/80 backdrop-blur-sm"
    >
      <div className="w-full max-w-md rounded-2xl border border-rose-500/30 bg-slate-900 shadow-2xl p-6 relative">
        <div className="flex items-center justify-between pb-4 border-b border-slate-800">
          <div className="flex items-center gap-2.5 text-rose-400">
            <XCircle className="h-5 w-5" />
            <h2 id="reject-job-title" className="text-base font-semibold text-white">
              Reject Work Item
            </h2>
          </div>
          <button
            onClick={onClose}
            aria-label="Close modal"
            className="rounded-lg p-1.5 text-slate-400 hover:text-white hover:bg-slate-800 transition-colors cursor-pointer"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="mt-4 space-y-4">
          <p className="text-xs text-slate-300">
            Rejecting this work item will route the job back into the Coding repair loop.
            Please provide specific guidance on what failed or needs correction (APR-6).
          </p>

          <div>
            <label htmlFor="reject-note" className="block text-xs font-medium text-slate-300 mb-1">
              Rejection Note <span className="text-rose-400">*</span>
            </label>
            <textarea
              id="reject-note"
              rows={4}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="e.g. Unit tests pass but edge case for empty strings is unhandled in parser..."
              disabled={loading}
              className="w-full rounded-xl border border-slate-700 bg-slate-950 px-3.5 py-2 text-sm text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-rose-500/30 font-sans"
            />
            {error && <p className="mt-1 text-xs text-rose-400">{error}</p>}
          </div>

          <div className="flex items-center justify-end gap-3 pt-3 border-t border-slate-800">
            <button
              type="button"
              onClick={onClose}
              className="rounded-xl px-4 py-2 text-xs font-medium text-slate-400 hover:text-white hover:bg-slate-800 transition-colors cursor-pointer"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={loading || !note.trim()}
              className="flex items-center gap-2 rounded-xl bg-rose-600 px-4 py-2 text-xs font-semibold text-white shadow-md shadow-rose-600/30 hover:bg-rose-500 disabled:opacity-50 transition-colors cursor-pointer"
            >
              {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Confirm Rejection
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
