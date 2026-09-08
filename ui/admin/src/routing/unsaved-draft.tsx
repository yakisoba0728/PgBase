import { createContext, useCallback, useContext, useEffect, useLayoutEffect, type ReactNode } from "react";

const leaveText = "Leave this page? The changes you have not saved will be lost.";

interface DraftLedger {
  held(): boolean;
  hold(resource: string, unsaved: boolean): void;
  confirmLeave(): boolean;
  mayLeave(): boolean;
}

export function draftLedger(confirmLeave: (text: string) => boolean = (text) => window.confirm(text)): DraftLedger {
  const unsaved = new Set<string>();
  const confirmed = () => unsaved.size === 0 || confirmLeave(leaveText);
  return {
    held: () => unsaved.size > 0,
    hold: (resource, dirty) => { if (dirty) unsaved.add(resource); else unsaved.delete(resource); },
    // Some departures, such as sign-out, can fail after the administrator
    // agrees to leave. Confirm them without releasing the guard; a successful
    // departure unmounts the resource and releases it in the ordinary way.
    confirmLeave: confirmed,
    // Nothing to lose is never worth a question. Agreeing to lose the draft is
    // the explicit discard, so the resource is released and the departure that
    // follows does not ask a second time about work already given up.
    mayLeave: () => {
      if (!confirmed()) return false;
      unsaved.clear();
      return true;
    },
  };
}

const LedgerContext = createContext<DraftLedger | null>(null);

export function UnsavedDrafts({ children, ledger }: { children: ReactNode; ledger: DraftLedger }) {
  useEffect(() => {
    // A reload or a closed tab leaves the application altogether, so only the
    // browser's own prompt can still offer to keep the draft.
    const ask = (event: BeforeUnloadEvent) => { if (ledger.held()) event.preventDefault(); };
    window.addEventListener("beforeunload", ask);
    return () => window.removeEventListener("beforeunload", ask);
  }, [ledger]);
  return <LedgerContext.Provider value={ledger}>{children}</LedgerContext.Provider>;
}

/**
 * Registers this resource's unsaved work and returns the release a completed
 * save performs before it navigates, so finishing the work never reads as
 * abandoning it.
 */
export function useUnsavedDraft(resource: string, unsaved: boolean): () => void {
  const ledger = useContext(LedgerContext);
  useLayoutEffect(() => {
    return () => ledger?.hold(resource, false);
  }, [ledger, resource]);
  useLayoutEffect(() => {
    ledger?.hold(resource, unsaved);
  }, [ledger, resource, unsaved]);
  return useCallback(() => ledger?.hold(resource, false), [ledger, resource]);
}
