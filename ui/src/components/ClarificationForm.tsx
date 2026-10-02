import { useState, useEffect, FormEvent } from 'react';
import { HelpCircle, Send, Loader2, AlertCircle } from 'lucide-react';
import { apiFetch, apiFetchText } from '../lib/api';
import { ClarificationAnswer } from '../types/api';

interface ClarificationFormProps {
  jobId: number;
  onSubmitted: () => void;
}

interface ParsedQuestion {
  q: number;
  text: string;
}

export function ClarificationForm({ jobId, onSubmitted }: ClarificationFormProps) {
  const [questions, setQuestions] = useState<ParsedQuestion[]>([]);
  const [answers, setAnswers] = useState<Record<number, string>>({});
  const [loading, setLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    async function loadQuestions() {
      try {
        setLoading(true);
        setError(null);
        const raw = await apiFetchText(`/api/jobs/${jobId}/artifacts/clarification-questions`);

        // Parse markdown questions
        const lines = raw.split('\n');
        const parsed: ParsedQuestion[] = [];
        let currentQ = 0;
        let buffer: string[] = [];

        for (const line of lines) {
          const match = line.match(/^(?:###\s*)?(?:Question\s*|Q)?(\d+)[:.]\s*(.*)/i);
          if (match) {
            if (currentQ > 0 && buffer.length > 0) {
              parsed.push({ q: currentQ, text: buffer.join(' ').trim() });
              buffer = [];
            }
            currentQ = Number(match[1]);
            if (match[2]) buffer.push(match[2]);
          } else if (currentQ > 0 && line.trim()) {
            buffer.push(line.trim());
          }
        }

        if (currentQ > 0 && buffer.length > 0) {
          parsed.push({ q: currentQ, text: buffer.join(' ').trim() });
        }

        // If no structured questions were found, treat whole text as Q1
        if (parsed.length === 0 && raw.trim()) {
          parsed.push({ q: 1, text: raw.trim() });
        }

        setQuestions(parsed);
      } catch (err: any) {
        setError('Failed to fetch clarification questions: ' + err.message);
      } finally {
        setLoading(false);
      }
    }

    loadQuestions();
  }, [jobId]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();

    const answerPayload: ClarificationAnswer[] = questions.map((item) => ({
      q: item.q,
      answer: (answers[item.q] || '').trim(),
    }));

    // Verify all questions have answers
    const unanswered = answerPayload.some((a) => !a.answer);
    if (unanswered) {
      setError('Please answer all questions before submitting.');
      return;
    }

    setSubmitting(true);
    setError(null);

    try {
      await apiFetch(`/api/jobs/${jobId}/clarification`, {
        method: 'POST',
        body: JSON.stringify({ answers: answerPayload }),
      });

      onSubmitted();
    } catch (err: any) {
      setError(err?.message || 'Failed to submit clarification answers');
    } finally {
      setSubmitting(false);
    }
  };

  if (loading) {
    return (
      <div className="rounded-2xl border border-amber-500/30 bg-amber-500/5 p-6 flex items-center justify-center gap-3 text-amber-300">
        <Loader2 className="h-5 w-5 animate-spin" />
        <span className="text-sm">Loading clarification questions...</span>
      </div>
    );
  }

  return (
    <div className="rounded-2xl border border-amber-500/40 bg-amber-500/10 p-6 shadow-lg">
      <div className="flex items-center gap-2.5 text-amber-300 mb-4 pb-3 border-b border-amber-500/20">
        <HelpCircle className="h-5 w-5 shrink-0" />
        <div>
          <h3 className="text-sm font-semibold text-white">Clarification Needed</h3>
          <p className="text-xs text-amber-300/80">
            The Spec Agent has identified ambiguities and requires answers to proceed (SPC-3).
          </p>
        </div>
      </div>

      <form onSubmit={handleSubmit} className="space-y-4">
        {error && (
          <div role="alert" className="flex items-center gap-2 rounded-xl border border-rose-500/30 bg-rose-500/10 p-3 text-xs text-rose-300">
            <AlertCircle className="h-4 w-4 shrink-0 text-rose-400" />
            <span>{error}</span>
          </div>
        )}

        <div className="space-y-4">
          {questions.map((q) => (
            <div key={q.q} className="rounded-xl border border-slate-800 bg-slate-900/80 p-4 space-y-2">
              <label htmlFor={`q-${q.q}`} className="block text-xs font-semibold text-indigo-300">
                Question {q.q}: <span className="text-slate-200 font-normal">{q.text}</span>
              </label>
              <textarea
                id={`q-${q.q}`}
                rows={3}
                value={answers[q.q] || ''}
                onChange={(e) => setAnswers({ ...answers, [q.q]: e.target.value })}
                placeholder="Provide your answer..."
                disabled={submitting}
                className="w-full rounded-xl border border-slate-700 bg-slate-950 p-3 text-xs text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-amber-500/30 font-sans"
              />
            </div>
          ))}
        </div>

        <div className="flex justify-end pt-2">
          <button
            type="submit"
            disabled={submitting || questions.length === 0}
            className="flex items-center gap-2 rounded-xl bg-amber-600 px-4 py-2 text-xs font-semibold text-white shadow-md shadow-amber-600/30 hover:bg-amber-500 disabled:opacity-50 transition-colors cursor-pointer"
          >
            {submitting ? (
              <>
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                Submitting answers...
              </>
            ) : (
              <>
                <Send className="h-3.5 w-3.5" />
                Submit Answers & Resume Spec Stage
              </>
            )}
          </button>
        </div>
      </form>
    </div>
  );
}
