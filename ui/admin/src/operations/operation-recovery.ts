import { useCallback, useEffect, useState } from "react";
import { useBackend } from "../auth/session";
import { SchemaError, type OperationStatus } from "../backend/ports";

interface OperationRecoveryTiming {
  readonly intervalMs: number;
  readonly maximumDurationMs: number;
  readonly readTimeoutMs: number;
  readonly initialNotFoundGraceMs: number;
}

// Backend schema applies may legitimately run for up to one hour. Keep a
// five-minute observation margin so the UI cannot give up while a permitted
// DDL is still running. The timing is injectable so boundary tests stay fast.
const operationRecoveryTiming: OperationRecoveryTiming = {
  intervalMs: 1000,
  maximumDurationMs: 65 * 60 * 1000,
  readTimeoutMs: 10 * 1000,
  initialNotFoundGraceMs: 5 * 1000,
};

function mayRetryFailure(error: unknown, now: number, notFoundUntil: number): boolean {
  if (!(error instanceof SchemaError)) return true;
  // A lost apply response can arrive before the request has durably claimed
  // its operation id, but that race is short. A persistently absent identity,
  // or any other typed contract failure, needs an explicit re-check rather
  // than thousands of automatic requests.
  if (error.reason === "not-found") return now < notFoundUntil;
  return error.reason === "failed" && ["internal_error", "schema_unavailable",
    "authentication_unavailable", "rate_limited"].includes(error.code);
}

// The ledger's terminal classifications. They are the whole failure vocabulary
// a receipt can carry, so each one names the next step it actually implies
// instead of one message that leaves the administrator guessing.
const operationFailureText: Record<string, string> = {
  plan_conflict: "The plan no longer matched the catalog, so nothing was applied. Preview the change again.",
  apply_failed: "The change did not apply and nothing was left behind.",
  lease_lost: "The operation was interrupted before it finished. Check schema operations before trying again.",
  reconcile_failed: "The operation was interrupted before it finished. Check schema operations before trying again.",
};

export function operationFailureMessage(failureCode: string | null | undefined, fallback: string): string {
  if (failureCode === null || failureCode === undefined) return fallback;
  return operationFailureText[failureCode] ?? fallback;
}

/** `unreadable` is a status that could not be fetched, which is not the same as
 * an operation the ledger reports as `unknown`: one is our failure to ask, the
 * other is the ledger's own answer. Neither may be read as a verdict. */
export type RecoveredStatus = OperationStatus | "unreadable";

export interface OperationRecovery {
  operationId: string;
  status: RecoveredStatus;
  /** True while the outcome is still genuinely undecided, so a view can say so
   * rather than choosing between success and failure. */
  undecided: boolean;
  checking: boolean;
  failureCode: string | null;
  recheck(): void;
}

interface RecoveryState {
  operationId: string | null;
  status: RecoveredStatus;
  failureCode: string | null;
  attempts: number;
  deadlineAt: number;
  notFoundUntil: number;
  stopped: boolean;
}

function started(operationId: string | null, timing: OperationRecoveryTiming): RecoveryState {
  const now = Date.now();
  return {
    operationId,
    status: "unknown",
    failureCode: null,
    attempts: 0,
    deadlineAt: now + timing.maximumDurationMs,
    notFoundUntil: now + timing.initialNotFoundGraceMs,
    stopped: false,
  };
}

/**
 * Turns an operation identity into the ledger's own verdict. It reads one
 * operation by id — never the history — so recovering a single apply cannot
 * become a fan-out, and it stops the moment the ledger commits or fails.
 */
export function useOperationRecovery(
  operationId: string | null,
  timing: OperationRecoveryTiming = operationRecoveryTiming,
): OperationRecovery | null {
  const { schema } = useBackend();
  const [state, setState] = useState<RecoveryState>(() => started(operationId, timing));
  const live = state.operationId === operationId ? state : started(operationId, timing);
  if (live !== state) setState(live);

  const settled = live.status === "committed" || live.status === "failed";
  const polling = operationId !== null && !settled && !live.stopped;

  useEffect(() => {
    if (!polling || operationId === null) return;
    const controller = new AbortController();
    let active = true;
    let readTimeout: ReturnType<typeof setTimeout> | undefined;
    // The first read goes out immediately; later reads use the ledger's retry
    // cadence without ever moving the absolute recovery deadline.
    const remaining = Math.max(0, live.deadlineAt - Date.now());
    const delay = live.attempts === 0 ? 0 : Math.min(timing.intervalMs, remaining);
    const timer = setTimeout(() => {
      const readWindow = live.deadlineAt - Date.now();
      if (readWindow <= 0) {
        setState((current) => current.operationId === operationId ? { ...current, stopped: true } : current);
        return;
      }
      let finished = false;
      const complete = (update: () => void) => {
        if (!active || finished) return;
        finished = true;
        if (readTimeout !== undefined) clearTimeout(readTimeout);
        update();
      };
      const unreadable = (error: unknown) => setState((current) => {
        if (current.operationId !== operationId) return current;
        const now = Date.now();
        return {
          ...current,
          status: "unreadable",
          attempts: current.attempts + 1,
          stopped: now >= current.deadlineAt || !mayRetryFailure(error, now, current.notFoundUntil),
        };
      });
      // The adapter receives the abort, while the local completion guard also
      // advances recovery if a non-conforming transport never rejects it.
      readTimeout = setTimeout(() => complete(() => {
        controller.abort();
        unreadable(undefined);
      }), Math.min(timing.readTimeoutMs, readWindow));
      void schema.operation(operationId, controller.signal)
        .then((receipt) => {
          complete(() => setState((current) => {
            if (current.operationId !== operationId) return current;
            const terminal = receipt.status === "committed" || receipt.status === "failed";
            return {
              ...current,
              status: receipt.status,
              failureCode: receipt.failureCode ?? null,
              attempts: current.attempts + 1,
              stopped: !terminal && Date.now() >= current.deadlineAt,
            };
          }));
        })
        .catch((error: unknown) => complete(() => unreadable(error)));
    }, delay);
    return () => {
      active = false;
      controller.abort();
      clearTimeout(timer);
      if (readTimeout !== undefined) clearTimeout(readTimeout);
    };
  }, [
    live.attempts,
    live.deadlineAt,
    operationId,
    polling,
    schema,
    timing.intervalMs,
    timing.readTimeoutMs,
  ]);

  const recheck = useCallback(
    () => setState(started(operationId, timing)),
    [operationId, timing],
  );

  if (operationId === null) return null;
  return {
    operationId,
    status: live.status,
    undecided: !settled,
    checking: polling,
    failureCode: live.failureCode,
    recheck,
  };
}
