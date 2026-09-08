import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { clearAdministratorRecordStorage } from "../workspace/record-productivity";
import { httpBackend } from "../backend/http-backend";
import { SessionError, type AdminBackend, type AuthSnapshot, type AuthenticatedSession, type LoginInput, type SessionFailure, type SetupInput } from "../backend/ports";
import { browserHistory, canonicalAdminPath, type AdminHistory } from "../routing/admin-location";

interface SessionContextValue {
  backend: AdminBackend;
  state: AuthSnapshot;
  sessionError: string | null;
  setup(input: SetupInput): Promise<void>;
  login(input: LoginInput): Promise<void>;
  logout(): Promise<void>;
}

const SessionContext = createContext<SessionContextValue | null>(null);

export function sessionFailure(error: unknown): SessionFailure {
  return error instanceof SessionError ? error.reason : "failed";
}

export function SessionProvider({ backend, children, history = browserHistory }: { backend?: AdminBackend; children: ReactNode; history?: AdminHistory }) {
  const [admin] = useState(() => backend ?? httpBackend());
  const [state, setState] = useState<AuthSnapshot>({ kind: "loading" });
  const [sessionError, setSessionError] = useState<string | null>(null);
  const transitionGeneration = useRef(0);
  const currentAdministrator = useRef<string | null>(null);
  const settle = useCallback((next: AuthSnapshot) => {
    const previous = currentAdministrator.current;
    const following = next.kind === "authenticated" ? next.administrator.id : null;
    if (previous !== null && previous !== following) clearAdministratorRecordStorage(previous);
    currentAdministrator.current = following;
    transitionGeneration.current += 1; setState(next);
    const screen = next.kind === "setup-required" ? "setup" : next.kind === "anonymous" ? "login" : "shell";
    const path = canonicalAdminPath(screen, history.path());
    if (history.path() !== path) history.replace(path);
  }, [history]);
  const open = useCallback(async (session: Promise<AuthenticatedSession>) => {
    const next = await session;
    setSessionError(null);
    settle({ kind: "authenticated", ...next });
  }, [settle]);

  useEffect(() => admin.session.onExpired(() => settle({ kind: "anonymous" })), [admin, settle]);

  useEffect(() => {
    const controller = new AbortController();
    void admin.session.resume(controller.signal).then((resumed) => {
      if (resumed.kind === "authenticated") { settle({ kind: "authenticated", ...resumed.session }); return; }
      if (resumed.kind === "setup-required") { settle(resumed); return; }
      if (resumed.kind === "setup-unknown") setSessionError("Unable to check administrator setup status. Please retry.");
      if (resumed.kind === "unreachable") setSessionError("Unable to check the current session. Please sign in.");
      settle({ kind: "anonymous" });
    }).catch(() => {
      if (controller.signal.aborted) return;
      setSessionError("Unable to check the current session. Please sign in.");
      settle({ kind: "anonymous" });
    });
    return () => controller.abort();
  }, [admin, settle]);

  useEffect(() => {
    if (state.kind !== "authenticated") return;
    const delay = Date.parse(state.expiresAt) - Date.now();
    const expire = () => settle({ kind: "anonymous" });
    if (!Number.isFinite(delay) || delay <= 0) { expire(); return; }
    const timer = window.setTimeout(expire, delay);
    return () => window.clearTimeout(timer);
  }, [settle, state]);

  const value = useMemo<SessionContextValue>(() => ({
    backend: admin,
    state,
    sessionError,
    setup: (input) => open(admin.session.setup(input)),
    login: (input) => open(admin.session.login(input)),
    logout: async () => {
      if (state.kind !== "authenticated") return;
      const generation = transitionGeneration.current; await admin.session.logout();
      if (generation === transitionGeneration.current) settle({ kind: "anonymous" });
    },
  }), [admin, open, sessionError, settle, state]);

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

function useSessionContext(): SessionContextValue {
  const value = useContext(SessionContext);
  if (value === null) throw new Error("useSession must be used inside SessionProvider");
  return value;
}

export function useSession(): Omit<SessionContextValue, "backend"> {
  return useSessionContext();
}

export function useBackend(): AdminBackend {
  return useSessionContext().backend;
}
