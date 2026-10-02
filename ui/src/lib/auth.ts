/**
 * Authentication and session management utilities for Garagefab dashboard (SEC-3).
 */

import { apiFetch } from './api';

export interface SessionResponse {
  status: string;
}

/**
 * Exchanges the secret API token for an HttpOnly browser session cookie.
 */
export async function exchangeApiToken(token: string): Promise<boolean> {
  try {
    await apiFetch<SessionResponse>('/api/session', {
      method: 'POST',
      body: JSON.stringify({ token }),
    });
    return true;
  } catch (err) {
    console.error('Failed to exchange API token for session:', err);
    return false;
  }
}

/**
 * Verifies if the current browser session is authenticated by probing a protected endpoint.
 */
export async function verifyCurrentSession(): Promise<boolean> {
  try {
    // Probing /api/projects requires either cookie or bearer token (SEC-3)
    await apiFetch('/api/projects');
    return true;
  } catch (err: any) {
    if (err?.status === 401 || err?.status === 403) {
      return false;
    }
    // Network errors or others: assume unauthenticated or service starting
    return false;
  }
}
