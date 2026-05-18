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

export async function api<T = any>(path: string, init: RequestInit = {}): Promise<T> {
  const a = getAuth();
  const headers = new Headers(init.headers);
  if (a) headers.set("Authorization", `Bearer ${a.access_token}`);
  const isFormData = typeof FormData !== "undefined" && init.body instanceof FormData;
  if (init.body && !headers.has("Content-Type") && !isFormData) headers.set("Content-Type", "application/json");
  const r = await fetch(path, { ...init, headers });
  if (r.status === 401) { clearAuth(); window.location.href = "/login"; throw new Error("unauthorized"); }
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
  const a = getAuth();
  const headers = new Headers(init.headers);
  if (a) headers.set("Authorization", `Bearer ${a.access_token}`);
  const isFormData = typeof FormData !== "undefined" && init.body instanceof FormData;
  if (init.body && !headers.has("Content-Type") && !isFormData) headers.set("Content-Type", "application/json");
  const r = await fetch(path, { ...init, headers });
  if (r.status === 401) { clearAuth(); window.location.href = "/login"; throw new Error("unauthorized"); }
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
