import { useEffect, useState } from 'react'
import { Activity, CheckCircle2, XCircle, Loader2 } from 'lucide-react'

type HealthStatus = 'loading' | 'ok' | 'error'

export default function App() {
  const [status, setStatus] = useState<HealthStatus>('loading')
  const [errorDetails, setErrorDetails] = useState<string>('')

  useEffect(() => {
    fetch('/api/health')
      .then(async (res) => {
        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`)
        }
        const data = await res.json()
        if (data.status === 'ok') {
          setStatus('ok')
        } else {
          setStatus('error')
          setErrorDetails('Invalid status response')
        }
      })
      .catch((err) => {
        setStatus('error')
        setErrorDetails(err.message || 'Unreachable')
      })
  }, [])

  return (
    <div className="flex min-h-screen flex-col items-center justify-center p-6 text-center">
      <div className="max-w-md w-full rounded-2xl border border-slate-800 bg-slate-900/60 p-8 shadow-2xl backdrop-blur">
        <div className="mx-auto mb-4 flex h-14 w-14 items-center justify-center rounded-2xl bg-indigo-500/10 text-indigo-400 border border-indigo-500/20">
          <Activity className="h-7 w-7" />
        </div>

        <h1 className="text-3xl font-bold tracking-tight text-white sm:text-4xl">
          Garagefab
        </h1>
        <p className="mt-2 text-sm text-slate-400">
          A lightweight, local-first software factory for solo developers.
        </p>

        <div className="mt-6 inline-flex items-center gap-2 rounded-full border px-4 py-1.5 text-sm font-medium transition-colors">
          {status === 'loading' && (
            <span className="flex items-center gap-2 text-slate-400 border-slate-700 bg-slate-800/40">
              <Loader2 className="h-4 w-4 animate-spin text-slate-400" />
              Checking service...
            </span>
          )}

          {status === 'ok' && (
            <span className="flex items-center gap-2 text-emerald-400 border-emerald-500/20 bg-emerald-500/10">
              <CheckCircle2 className="h-4 w-4 text-emerald-400" />
              Service: OK
            </span>
          )}

          {status === 'error' && (
            <span className="flex items-center gap-2 text-rose-400 border-rose-500/20 bg-rose-500/10">
              <XCircle className="h-4 w-4 text-rose-400" />
              Service: unreachable ({errorDetails})
            </span>
          )}
        </div>

        <div className="mt-8 border-t border-slate-800 pt-4 text-xs text-slate-500">
          Milestone M0 — Foundation Placeholder UI
        </div>
      </div>
    </div>
  )
}
