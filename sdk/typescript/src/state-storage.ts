import type { RecordData } from "./types.js";

export type AuthState = { token: string | null; record: RecordData | null };

/** Opt-in synchronous storage for a complete auth state, separate from token-only storage. */
export interface AuthStateStorage {
  read(): AuthState | null;
  write(state: AuthState): void;
  clear(): void;
}

/** Select browser storage deliberately; the default AuthStore remains memory-only. */
export function browserAuthStorage(storage: Storage, key = "pgbase.user.auth"): AuthStateStorage {
  return {
    read: () => {
      const raw = storage.getItem(key);
      if (raw === null) return null;
      try { return authState(JSON.parse(raw)); }
      catch { return null; }
    },
    write: (state) => storage.setItem(key, JSON.stringify(state)),
    clear: () => storage.removeItem(key),
  };
}

/** Snapshot stored JSON data so callers cannot mutate the restored store through storage. */
export function authState(value: unknown): AuthState | null {
  if (!value || typeof value !== "object") return null;
  const state = value as Partial<AuthState>;
  if (state.token !== null && typeof state.token !== "string") return null;
  if (state.record !== null && (typeof state.record !== "object" || Array.isArray(state.record))) return null;
  if (!state.token) return { token: null, record: null };
  return JSON.parse(JSON.stringify({ token: state.token, record: state.record ?? null })) as AuthState;
}
