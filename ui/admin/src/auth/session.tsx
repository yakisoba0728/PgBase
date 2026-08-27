import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
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
  };
}

function updatePath(state: SessionState): void {
  const path = state.kind === "setup-required" ? "/admin/setup" :
    state.kind === "anonymous" ? "/admin/login" :
      window.location.pathname.startsWith("/admin/") && !["/admin/login", "/admin/setup"].includes(window.location.pathname) ? window.location.pathname : "/admin/collections";
  if (window.location.pathname !== path) window.history.replaceState(null, "", path);
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const client = useMemo(() => new APIClient(), []);
  const [state, setState] = useState<SessionState>({ kind: "loading" });
  const [sessionError, setSessionError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void client.getSession(controller.signal).then((session) => {
      const next = authenticatedState(session);
      setState(next);
      updatePath(next);
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      if (error instanceof APIClientError && error.status === 404 && error.code === "setup_required") {
        setState({ kind: "setup-required" });
        updatePath({ kind: "setup-required" });
        return;
      }
      setState({ kind: "anonymous" });
      updatePath({ kind: "anonymous" });
      if (!(error instanceof APIClientError && error.status === 401)) setSessionError("Unable to check the current session. Please sign in.");
    });
    return () => controller.abort();
  }, [client]);

  const setup = useCallback(async (input: SetupInput) => {
    const session = await client.setup(input);
    const next = authenticatedState(session);
    setSessionError(null);
    setState(next);
    updatePath(next);
  }, [client]);

  const login = useCallback(async (input: LoginInput) => {
    const session = await client.login(input);
    const next = authenticatedState(session);
    setSessionError(null);
    setState(next);
    updatePath(next);
  }, [client]);

  const logout = useCallback(async () => {
    if (state.kind !== "authenticated") return;
    await client.logout(state.csrfToken);
    const next: SessionState = { kind: "anonymous" };
    setState(next);
    updatePath(next);
  }, [client, state]);

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
