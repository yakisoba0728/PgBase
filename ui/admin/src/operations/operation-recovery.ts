import { useCallback, useEffect, useState } from "react";
import { useBackend } from "../auth/session";
import type { OperationStatus } from "../backend/ports";

// A DDL that outlived the request deadline is still running when the first
// status read lands, so one read cannot decide it. The budget stops a
// transaction that outlives any plausible DDL from polling for ever and hands
// the administrator an explicit re-check instead of an endless spinner.
const recoveryBudget = 30;
const recoveryIntervalMs = 1000;

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
  reads: number;
}

const started = (operationId: string | null): RecoveryState => ({ operationId, status: "unknown", failureCode: null, reads: 0 });

/**
 * Turns an operation identity into the ledger's own verdict. It reads one
 * operation by id — never the history — so recovering a single apply cannot
 * become a fan-out, and it stops the moment the ledger commits or fails.
 */
export function useOperationRecovery(operationId: string | null): OperationRecovery | null {
  const { schema } = useBackend();
  const [state, setState] = useState<RecoveryState>(() => started(operationId));
  const live = state.operationId === operationId ? state : started(operationId);
  if (live !== state) setState(live);

  const settled = live.status === "committed" || live.status === "failed";
  const polling = operationId !== null && !settled && live.status !== "unreadable" && live.reads < recoveryBudget;

  useEffect(() => {
    if (!polling || operationId === null) return;
    const controller = new AbortController();
    // The first read goes out immediately; the ledger's own Retry-After paces
    // the rest, and each answer schedules the next through `reads`.
    const timer = setTimeout(() => {
      void schema.operation(operationId, controller.signal)
        .then((receipt) => {
          if (controller.signal.aborted) return;
          setState((current) => current.operationId !== operationId
            ? current
            : { ...current, status: receipt.status, failureCode: receipt.failureCode ?? null, reads: current.reads + 1 });
        })
        .catch(() => {
          if (controller.signal.aborted) return;
          setState((current) => current.operationId !== operationId ? current : { ...current, status: "unreadable" });
        });
    }, live.reads === 0 ? 0 : recoveryIntervalMs);
    return () => { controller.abort(); clearTimeout(timer); };
  }, [live.reads, operationId, polling, schema]);

  const recheck = useCallback(() => setState((current) => ({ ...current, status: "unknown", failureCode: null, reads: 0 })), []);

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
