/**
 * Lightweight client-side router for Garagefab React SPA.
 * Works natively with Go embedded static file server fallback (spec §7).
 */

import React, { createContext, useContext, useEffect, useState } from 'react';

interface RouterContextType {
  path: string;
  navigate: (to: string) => void;
  params: Record<string, string>;
}

const RouterContext = createContext<RouterContextType>({
  path: window.location.pathname,
  navigate: () => {},
  params: {},
});

export function useRouter() {
  return useContext(RouterContext);
}

export function RouterProvider({ children }: { children: React.ReactNode }) {
  const [path, setPath] = useState(window.location.pathname || '/');

  useEffect(() => {
    const handlePopState = () => {
      setPath(window.location.pathname || '/');
    };

    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, []);

  const navigate = (to: string) => {
    if (to !== window.location.pathname) {
      window.history.pushState(null, '', to);
      setPath(to);
    }
  };

  // Simple route parameter extractor for routes like /jobs/:id
  const params: Record<string, string> = {};
  if (path.startsWith('/jobs/')) {
    const parts = path.split('/');
    if (parts.length >= 3 && parts[2]) {
      params.id = parts[2];
    }
  }

  return (
    <RouterContext.Provider value={{ path, navigate, params }}>
      {children}
    </RouterContext.Provider>
  );
}

export function Link({
  href,
  children,
  className,
  title,
}: {
  href: string;
  children: React.ReactNode;
  className?: string;
  title?: string;
}) {
  const { navigate } = useRouter();

  return (
    <a
      href={href}
      title={title}
      className={className}
      onClick={(e) => {
        // Allow ctrl+click or meta+click to open in new tab
        if (!e.ctrlKey && !e.metaKey && !e.shiftKey) {
          e.preventDefault();
          navigate(href);
        }
      }}
    >
      {children}
    </a>
  );
}
