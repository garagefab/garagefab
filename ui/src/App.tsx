import { useEffect, useState, useCallback } from 'react';
import {
  Layers,
  Kanban,
  PlusCircle,
  FolderPlus,
  Loader2,
  Radio,
} from 'lucide-react';
import { RouterProvider, useRouter, Link } from './lib/router';
import { verifyCurrentSession } from './lib/auth';
import { apiFetch } from './lib/api';
import { Project, StepRun } from './types/api';
import { useSSE } from './hooks/useSSE';
import { LoginPage } from './pages/LoginPage';
import { OverviewPage } from './pages/OverviewPage';
import { BoardPage } from './pages/BoardPage';
import { JobDetailPage } from './pages/JobDetailPage';
import { AddProjectModal } from './components/AddProjectModal';
import { NewJobModal } from './components/NewJobModal';
import { LogViewerModal } from './components/LogViewerModal';

function AppContent() {
  const { path, params, navigate } = useRouter();

  const [isAuthenticated, setIsAuthenticated] = useState<boolean | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [openNewJob, setOpenNewJob] = useState(false);
  const [openAddProject, setOpenAddProject] = useState(false);
  const [logStep, setLogStep] = useState<StepRun | null>(null);
  const [logJobId, setLogJobId] = useState<number>(0);

  // Check existing session or URL hash token on initial mount
  useEffect(() => {
    const hash = window.location.hash;
    if (hash.includes('token=')) {
      // Let LoginPage handle token extraction & cookie exchange (SEC-3, CLI-2)
      setIsAuthenticated(false);
      return;
    }

    verifyCurrentSession().then((authenticated) => {
      setIsAuthenticated(authenticated);
    });
  }, []);

  const loadProjects = useCallback(async () => {
    try {
      const data = await apiFetch<Project[]>('/api/projects');
      setProjects(data);
    } catch {
      // Ignore if unauthenticated
    }
  }, []);

  useEffect(() => {
    if (isAuthenticated) {
      loadProjects();
    }
  }, [isAuthenticated, loadProjects]);

  // Connect to SSE event stream for live reactive updates (UI-5, LOG-4)
  const { isConnected } = useSSE(() => {
    // If a project is created or updated, refresh projects list
    loadProjects();
  });

  if (isAuthenticated === null) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-slate-950 text-slate-100">
        <div className="flex flex-col items-center gap-3">
          <Loader2 className="h-8 w-8 animate-spin text-indigo-500" />
          <p className="text-sm text-slate-400 font-medium">Verifying session...</p>
        </div>
      </div>
    );
  }

  if (!isAuthenticated) {
    return (
      <LoginPage
        onLoginSuccess={() => {
          setIsAuthenticated(true);
          loadProjects();
        }}
      />
    );
  }

  // Routing dispatch
  const renderCurrentView = () => {
    if (path === '/') {
      return (
        <OverviewPage
          onOpenNewJob={() => setOpenNewJob(true)}
          onOpenAddProject={() => setOpenAddProject(true)}
        />
      );
    }

    if (path === '/board') {
      return <BoardPage onOpenNewJob={() => setOpenNewJob(true)} />;
    }

    if (path.startsWith('/jobs/') && params.id) {
      const parsedId = parseInt(params.id, 10);
      if (!isNaN(parsedId)) {
        return (
          <JobDetailPage
            jobId={parsedId}
            onOpenLogs={(step) => {
              setLogJobId(parsedId);
              setLogStep(step);
            }}
          />
        );
      }
    }

    // Default fallback to Overview
    return (
      <OverviewPage
        onOpenNewJob={() => setOpenNewJob(true)}
        onOpenAddProject={() => setOpenAddProject(true)}
      />
    );
  };

  return (
    <div className="min-h-screen flex flex-col bg-slate-950 text-slate-100 font-sans selection:bg-indigo-500/30">
      {/* Top Application Header / Navbar */}
      <header className="sticky top-0 z-40 flex h-16 items-center justify-between border-b border-slate-800 bg-slate-950/80 px-6 backdrop-blur">
        {/* Left: Branding & Core Navigation */}
        <div className="flex items-center gap-8">
          <Link
            href="/"
            className="flex items-center gap-3 text-white font-bold text-lg tracking-tight hover:opacity-90 transition-opacity focus-visible:outline-2 focus-visible:outline-indigo-500 focus-visible:outline-offset-2 rounded-lg"
          >
            <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-indigo-600 text-white shadow-md shadow-indigo-600/30">
              <Layers className="h-5 w-5" />
            </div>
            <span>Garagefab</span>
          </Link>

          <nav className="flex items-center gap-1" aria-label="Main Navigation">
            <Link
              href="/"
              className={`flex items-center gap-2 rounded-xl px-3.5 py-2 text-xs font-semibold transition-all focus-visible:outline-2 focus-visible:outline-indigo-500 focus-visible:outline-offset-2 ${
                path === '/'
                  ? 'bg-slate-800/90 text-white shadow-inner'
                  : 'text-slate-400 hover:text-white hover:bg-slate-800/50'
              }`}
            >
              <Layers className="h-4 w-4" />
              <span>Overview</span>
            </Link>

            <Link
              href="/board"
              className={`flex items-center gap-2 rounded-xl px-3.5 py-2 text-xs font-semibold transition-all focus-visible:outline-2 focus-visible:outline-indigo-500 focus-visible:outline-offset-2 ${
                path === '/board'
                  ? 'bg-slate-800/90 text-white shadow-inner'
                  : 'text-slate-400 hover:text-white hover:bg-slate-800/50'
              }`}
            >
              <Kanban className="h-4 w-4" />
              <span>Board</span>
            </Link>
          </nav>
        </div>

        {/* Right: Real-time Connection Indicator & Action Buttons */}
        <div className="flex items-center gap-4">
          {/* Live SSE Connection Status (UI-5) */}
          <div
            className={`flex items-center gap-2 px-3 py-1.5 rounded-full border text-xs font-medium transition-colors ${
              isConnected
                ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-400'
                : 'border-amber-500/20 bg-amber-500/10 text-amber-400'
            }`}
            title={isConnected ? 'Connected to live events stream' : 'Reconnecting to live events stream...'}
          >
            <Radio className={`h-3.5 w-3.5 ${isConnected ? 'animate-pulse text-emerald-400' : 'text-amber-400'}`} />
            <span>{isConnected ? 'Live' : 'Connecting...'}</span>
          </div>

          {/* Add Project Button (UI-6, PRJ-1) */}
          <button
            type="button"
            onClick={() => setOpenAddProject(true)}
            className="flex items-center gap-2 rounded-xl border border-slate-700 bg-slate-800/80 px-3.5 py-2 text-xs font-semibold text-slate-200 hover:bg-slate-800 hover:text-white transition-all focus-visible:outline-2 focus-visible:outline-indigo-500 focus-visible:outline-offset-2 cursor-pointer"
          >
            <FolderPlus className="h-4 w-4 text-slate-400" />
            <span>Add Project</span>
          </button>

          {/* New Job Button (INT-1) */}
          <button
            type="button"
            onClick={() => setOpenNewJob(true)}
            className="flex items-center gap-2 rounded-xl bg-indigo-600 px-4 py-2 text-xs font-semibold text-white shadow-md shadow-indigo-600/30 hover:bg-indigo-500 transition-all focus-visible:outline-2 focus-visible:outline-indigo-500 focus-visible:outline-offset-2 cursor-pointer"
          >
            <PlusCircle className="h-4 w-4" />
            <span>New Job</span>
          </button>
        </div>
      </header>

      {/* Main Page Content */}
      <main className="flex-1 flex flex-col">{renderCurrentView()}</main>

      {/* Modals mounted at root level */}
      <AddProjectModal
        isOpen={openAddProject}
        onClose={() => setOpenAddProject(false)}
        onProjectAdded={() => {
          loadProjects();
          setOpenAddProject(false);
        }}
      />

      <NewJobModal
        isOpen={openNewJob}
        projects={projects}
        onClose={() => setOpenNewJob(false)}
        onJobCreated={(job) => {
          setOpenNewJob(false);
          navigate(`/jobs/${job.id}`);
        }}
      />

      <LogViewerModal
        isOpen={logStep !== null}
        jobId={logJobId}
        step={logStep}
        onClose={() => setLogStep(null)}
      />
    </div>
  );
}

export default function App() {
  return (
    <RouterProvider>
      <AppContent />
    </RouterProvider>
  );
}
