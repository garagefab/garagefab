import { useEffect, useState, FormEvent } from 'react';
import { KeyRound, Loader2, AlertCircle, ArrowRight } from 'lucide-react';
import { exchangeApiToken } from '../lib/auth';
import { useRouter } from '../lib/router';

interface LoginPageProps {
  onLoginSuccess: () => void;
}

export function LoginPage({ onLoginSuccess }: LoginPageProps) {
  const { navigate } = useRouter();
  const [tokenInput, setTokenInput] = useState('');
  const [loading, setLoading] = useState(false);
  const [autoExchanging, setAutoExchanging] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  useEffect(() => {
    // 1. Extract #token=<token> from URL hash fragment (spec SEC-3, CLI-2)
    const hash = window.location.hash;
    let tokenFromHash = '';
    if (hash.startsWith('#token=')) {
      tokenFromHash = hash.substring(7);
    } else if (hash.includes('token=')) {
      const match = hash.match(/token=([a-zA-Z0-9_-]+)/);
      if (match && match[1]) {
        tokenFromHash = match[1];
      }
    }

    if (tokenFromHash) {
      setAutoExchanging(true);
      exchangeApiToken(tokenFromHash)
        .then((ok) => {
          if (ok) {
            // Strip token from browser address bar immediately without reloading (SEC-3)
            window.history.replaceState(null, '', window.location.pathname);
            onLoginSuccess();
            navigate('/');
          } else {
            setErrorMessage('Invalid or expired API token from URL');
          }
        })
        .finally(() => {
          setAutoExchanging(false);
        });
    }
  }, [navigate, onLoginSuccess]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!tokenInput.trim()) {
      setErrorMessage('Please enter your API token');
      return;
    }

    setLoading(true);
    setErrorMessage(null);

    const success = await exchangeApiToken(tokenInput.trim());
    setLoading(false);

    if (success) {
      onLoginSuccess();
      navigate('/');
    } else {
      setErrorMessage('Authentication failed. Check your API token or run "garagefab open".');
    }
  };

  return (
    <div className="flex min-h-screen flex-col items-center justify-center p-6 bg-slate-950 text-slate-100">
      <div className="w-full max-w-md rounded-2xl border border-slate-800 bg-slate-900/80 p-8 shadow-2xl backdrop-blur">
        <div className="mx-auto mb-6 flex h-14 w-14 items-center justify-center rounded-2xl bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
          <KeyRound className="h-7 w-7" />
        </div>

        <h1 className="text-2xl font-bold tracking-tight text-center text-white sm:text-3xl">
          Garagefab Login
        </h1>
        <p className="mt-2 text-sm text-center text-slate-400">
          Enter your API token or run <code className="text-indigo-300 bg-slate-800 px-1.5 py-0.5 rounded text-xs font-mono">garagefab open</code> in your terminal.
        </p>

        {autoExchanging ? (
          <div className="mt-8 flex flex-col items-center justify-center gap-3 py-6">
            <Loader2 className="h-8 w-8 animate-spin text-indigo-400" />
            <p className="text-sm text-slate-400 font-medium">Authenticating via URL token...</p>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="mt-6 space-y-4">
            {errorMessage && (
              <div
                role="alert"
                className="flex items-start gap-2.5 rounded-xl border border-rose-500/30 bg-rose-500/10 p-3.5 text-xs text-rose-300"
              >
                <AlertCircle className="h-4 w-4 shrink-0 mt-0.5 text-rose-400" />
                <span>{errorMessage}</span>
              </div>
            )}

            <div>
              <label htmlFor="api-token" className="block text-xs font-medium text-slate-300 mb-1.5">
                API Token
              </label>
              <input
                id="api-token"
                type="password"
                value={tokenInput}
                onChange={(e) => setTokenInput(e.target.value)}
                placeholder="Paste 64-character token..."
                disabled={loading}
                className="w-full rounded-xl border border-slate-700 bg-slate-950 px-3.5 py-2.5 text-sm text-white placeholder-slate-500 focus:border-indigo-500 focus:outline-none focus:ring-2 focus:ring-indigo-500/20 disabled:opacity-50 transition-colors font-mono"
              />
            </div>

            <button
              type="submit"
              disabled={loading}
              className="w-full flex items-center justify-center gap-2 rounded-xl bg-indigo-600 px-4 py-2.5 text-sm font-semibold text-white shadow-lg shadow-indigo-600/30 hover:bg-indigo-500 focus-visible:outline-2 focus-visible:outline-indigo-500 disabled:opacity-50 transition-all cursor-pointer"
            >
              {loading ? (
                <>
                  <Loader2 className="h-4 w-4 animate-spin" />
                  Verifying...
                </>
              ) : (
                <>
                  Sign In
                  <ArrowRight className="h-4 w-4" />
                </>
              )}
            </button>
          </form>
        )}

        <div className="mt-8 border-t border-slate-800/80 pt-4 text-center text-xs text-slate-500">
          Secured with HttpOnly session cookies (SEC-3)
        </div>
      </div>
    </div>
  );
}
