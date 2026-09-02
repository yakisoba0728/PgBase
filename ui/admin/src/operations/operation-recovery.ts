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
