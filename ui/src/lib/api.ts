/**
 * Lightweight HTTP client wrapper for Garagefab API endpoints.
 * Automatically sends credentials: 'include' for secure cookie authentication (SEC-3).
 */

export class ApiRequestError extends Error {
  code: string;
  status: number;
  details?: Record<string, string>;

  constructor(status: number, message: string, code: string = 'internal', details?: Record<string, string>) {
    super(message);
    this.name = 'ApiRequestError';
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

export async function apiFetch<T>(url: string, options: RequestInit = {}): Promise<T> {
  const defaultHeaders: HeadersInit = {
    Accept: 'application/json',
  };

  if (options.body && typeof options.body === 'string') {
    defaultHeaders['Content-Type'] = 'application/json';
  }

  const response = await fetch(url, {
    ...options,
    credentials: 'include', // Ensure browser session cookie is transmitted (SEC-3)
    headers: {
      ...defaultHeaders,
      ...options.headers,
    },
  });

  if (!response.ok) {
    let errorMsg = `HTTP ${response.status} ${response.statusText}`;
    let errorCode = 'unknown';
    let details: Record<string, string> | undefined;

    try {
      const errorJson = await response.json();
      if (errorJson?.error) {
        errorMsg = errorJson.error.message || errorMsg;
        errorCode = errorJson.error.code || errorCode;
        details = errorJson.error.details;
      } else if (typeof errorJson === 'string') {
        errorMsg = errorJson;
      }
    } catch {
      // Body was not JSON
    }

    throw new ApiRequestError(response.status, errorMsg, errorCode, details);
  }

  // Handle empty responses (e.g. 204 No Content)
  if (response.status === 204) {
    return {} as T;
  }

  return response.json() as Promise<T>;
}

export async function apiFetchText(url: string, options: RequestInit = {}): Promise<string> {
  const response = await fetch(url, {
    ...options,
    credentials: 'include',
  });

  if (!response.ok) {
    throw new ApiRequestError(response.status, `HTTP ${response.status}`);
  }

  return response.text();
}
