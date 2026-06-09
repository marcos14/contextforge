import { Navigate, useLocation } from "react-router-dom";
import { ReactNode } from "react";

const KEY = "mcpb_auth";

export type Auth = { access_token: string; refresh_token: string; role: string; user_id: string; email?: string };

export function setAuth(a: Auth) { localStorage.setItem(KEY, JSON.stringify(a)); }
export function getAuth(): Auth | null {
  const raw = localStorage.getItem(KEY);
  return raw ? JSON.parse(raw) : null;
}
export function clearAuth() { localStorage.removeItem(KEY); }

// Single in-flight refresh promise so concurrent 401s trigger only one
// /api/auth/refresh call (avoids a refresh stampede).
let refreshPromise: Promise<string | null> | null = null;

// refreshAccessToken trades the stored refresh_token for a fresh access token,
// updates localStorage in place, and returns the new token (or null on failure).
function refreshAccessToken(): Promise<string | null> {
  const a = getAuth();
  if (!a?.refresh_token) return Promise.resolve(null);
  if (!refreshPromise) {
    refreshPromise = fetch("/api/auth/refresh", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: a.refresh_token }),
    })
      .then(async (r) => {
        if (!r.ok) return null;
        const data = await r.json().catch(() => null);
        const cur = getAuth();
        if (cur && data?.access_token) {
          setAuth({ ...cur, access_token: data.access_token });
          return data.access_token as string;
        }
        return null;
      })
      .catch(() => null)
      .finally(() => { refreshPromise = null; });
  }
  return refreshPromise;
}

// authedFetch performs a fetch with the Authorization header injected. On a 401
// it attempts a single token refresh and retries the request once; if it still
// fails, the session is cleared and the user is redirected to /login.
async function authedFetch(path: string, init: RequestInit): Promise<Response> {
  // Headers are rebuilt per attempt so the retry picks up the refreshed token.
  const buildHeaders = () => {
    const a = getAuth();
    const headers = new Headers(init.headers);
    if (a) headers.set("Authorization", `Bearer ${a.access_token}`);
    const isFormData = typeof FormData !== "undefined" && init.body instanceof FormData;
    if (init.body && !headers.has("Content-Type") && !isFormData) headers.set("Content-Type", "application/json");
    return headers;
  };
  let r = await fetch(path, { ...init, headers: buildHeaders() });
  if (r.status === 401) {
    const token = await refreshAccessToken();
    if (token) r = await fetch(path, { ...init, headers: buildHeaders() });
    if (r.status === 401) { clearAuth(); window.location.href = "/login"; throw new Error("unauthorized"); }
  }
  return r;
}

export async function api<T = any>(path: string, init: RequestInit = {}): Promise<T> {
  const r = await authedFetch(path, init);
  if (!r.ok) {
    const err = await r.json().catch(() => ({ error: r.statusText }));
    throw new Error(err.error || r.statusText);
  }
  if (r.status === 204) return undefined as T;
  // Tolerate empty bodies (e.g. 201 Created with no JSON payload).
  const text = await r.text();
  if (!text) return undefined as T;
  try {
    return JSON.parse(text) as T;
  } catch {
    return undefined as T;
  }
}

// apiBlob fetches a binary response (e.g. file download) and returns the raw
// Blob. Auth header is injected like in api(). Throws on non-OK.
export async function apiBlob(path: string, init: RequestInit = {}): Promise<Blob> {
  const r = await authedFetch(path, init);
  if (!r.ok) {
    const err = await r.json().catch(() => ({ error: r.statusText }));
    throw new Error(err.error || r.statusText);
  }
  return await r.blob();
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const loc = useLocation();
  if (!getAuth()) return <Navigate to="/login" state={{ from: loc }} replace />;
  return <>{children}</>;
}
