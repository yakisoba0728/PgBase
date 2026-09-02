import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { APIClient, APIClientError } from "../api/client";
import { CollectionsAPI } from "../api/collections";
import { RecordsAPI } from "../api/records";
import type { LoginInput, SessionResponse, SessionState, SetupInput } from "../api/types";

interface SessionContextValue {
  client: APIClient;
  collections: CollectionsAPI;
  records: RecordsAPI;
  state: SessionState;
  sessionError: string | null;
  setup(input: SetupInput): Promise<void>;
  login(input: LoginInput): Promise<void>;
  logout(): Promise<void>;
}

const SessionContext = createContext<SessionContextValue | null>(null);

function authenticatedState(session: SessionResponse): SessionState {
  return {
    kind: "authenticated",
    administrator: session.administrator,
    csrfToken: session.csrfToken,
    expiresAt: session.expiresAt,
  };
}

function updatePath(state: SessionState): void {
  const path = state.kind === "setup-required" ? "/admin/setup" :
    state.kind === "anonymous" ? "/admin/login" :
      window.location.pathname.startsWith("/admin/") && !["/admin/login", "/admin/setup"].includes(window.location.pathname) ? window.location.pathname : "/admin/collections";
  if (window.location.pathname !== path) window.history.replaceState(null, "", path);
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<SessionState>({ kind: "loading" });
  const [sessionError, setSessionError] = useState<string | null>(null);
  const transitionGeneration = useRef(0);
  const settle = useCallback((next: SessionState) => {
    transitionGeneration.current += 1;
    setState(next);
    updatePath(next);
  }, []);
  const client = useMemo(() => new APIClient(() => settle({ kind: "anonymous" })), [settle]);

  useEffect(() => {
    const controller = new AbortController();
    void client.getSession(controller.signal).then((session) => {
      settle(authenticatedState(session));
    }).catch(async (error: unknown) => {
      if (controller.signal.aborted) return;
      if (error instanceof APIClientError && error.status === 404 && error.code === "setup_required") {
        settle({ kind: "setup-required" });
        return;
      }
      if (error instanceof APIClientError && error.status === 401) {
        try {
          const status = await client.getAdminStatus(controller.signal);
          const next: SessionState = status.setupRequired ? { kind: "setup-required" } : { kind: "anonymous" };
          settle(next);
          return;
        } catch {
          if (controller.signal.aborted) return;
          setSessionError("Unable to check administrator setup status. Please retry.");
        }
      }
      settle({ kind: "anonymous" });
      if (!(error instanceof APIClientError && error.status === 401)) setSessionError("Unable to check the current session. Please sign in.");
    });
    return () => controller.abort();
  }, [client, settle]);

  useEffect(() => {
    if (state.kind !== "authenticated") return;
    const delay = Date.parse(state.expiresAt) - Date.now();
    const expire = () => settle({ kind: "anonymous" });
    if (!Number.isFinite(delay) || delay <= 0) { expire(); return; }
    const timer = window.setTimeout(expire, delay);
    return () => window.clearTimeout(timer);
  }, [settle, state]);

  const setup = useCallback(async (input: SetupInput) => {
    const session = await client.setup(input);
    setSessionError(null);
    settle(authenticatedState(session));
  }, [client, settle]);

  const login = useCallback(async (input: LoginInput) => {
    const session = await client.login(input);
    setSessionError(null);
    settle(authenticatedState(session));
  }, [client, settle]);

  const logout = useCallback(async () => {
    if (state.kind !== "authenticated") return;
    const generation = transitionGeneration.current;
    await client.logout(state.csrfToken);
    if (generation === transitionGeneration.current) settle({ kind: "anonymous" });
  }, [client, settle, state]);

  const csrfToken = state.kind === "authenticated" ? state.csrfToken : "";
  const collections = useMemo(() => new CollectionsAPI(client, csrfToken), [client, csrfToken]);
  const records = useMemo(() => new RecordsAPI(client, csrfToken), [client, csrfToken]);

  return <SessionContext.Provider value={{ client, collections, records, state, sessionError, setup, login, logout }}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (value === null) throw new Error("useSession must be used inside SessionProvider");
  return value;
}
