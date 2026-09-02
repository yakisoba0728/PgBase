import { createContext, useCallback, useContext, useEffect, type ReactNode } from "react";

const leaveText = "Leave this page? The changes you have not saved will be lost.";

interface DraftLedger {
  held(): boolean;
  hold(resource: string, unsaved: boolean): void;
  mayLeave(): boolean;
}

export function draftLedger(confirmLeave: (text: string) => boolean = (text) => window.confirm(text)): DraftLedger {
  const unsaved = new Set<string>();
  return {
    held: () => unsaved.size > 0,
    hold: (resource, dirty) => { if (dirty) unsaved.add(resource); else unsaved.delete(resource); },
    // Nothing to lose is never worth a question. Agreeing to lose the draft is
    // the explicit discard, so the resource is released and the departure that
    // follows does not ask a second time about work already given up.
    mayLeave: () => {
      if (unsaved.size === 0) return true;
      if (!confirmLeave(leaveText)) return false;
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
  useEffect(() => {
    ledger?.hold(resource, unsaved);
    return () => ledger?.hold(resource, false);
  }, [ledger, resource, unsaved]);
  return useCallback(() => ledger?.hold(resource, false), [ledger, resource]);
}
