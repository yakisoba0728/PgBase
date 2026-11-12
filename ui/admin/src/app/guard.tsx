import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import type { SetupStatus } from "../api/system";
import { ErrorState, Loader } from "../components/Feedback";
import { useT } from "../i18n";
import { isUnauthorized, sessionQuery, setupStatusQuery } from "./queries";

interface QueryState<T> {
  data?: T;
  error: unknown;
  isPending: boolean;
}

type GuardDecision = { kind: "ready" | "loading" | "error" } | { kind: "redirect"; to: "/login" | "/setup" };

/** No session (401) → /setup while first-run setup is pending, else /login. Other failures are errors, not redirects. */
export function decideGuard(session: QueryState<unknown>, status: QueryState<SetupStatus>): GuardDecision {
  if (session.data) return { kind: "ready" };
  if (session.isPending) return { kind: "loading" };
  if (!isUnauthorized(session.error)) return { kind: "error" };
  if (status.data) return { kind: "redirect", to: status.data.setupRequired ? "/setup" : "/login" };
  if (status.error) return { kind: "redirect", to: "/login" };
  return { kind: "loading" };
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const t = useT();
  const location = useLocation();
  const session = useQuery(sessionQuery);
  const status = useQuery({ ...setupStatusQuery, enabled: isUnauthorized(session.error) });
  const decision = decideGuard(session, status);
  switch (decision.kind) {
    case "ready":
      return children;
    case "loading":
      return <Loader />;
    case "redirect":
      return <Navigate to={decision.to} replace state={{ from: location.pathname + location.search + location.hash }} />;
    case "error":
      return (
        <ErrorState busy={session.isFetching} onRetry={session.refetch}>
          {t("guard.unreachable")}
        </ErrorState>
      );
  }
}
