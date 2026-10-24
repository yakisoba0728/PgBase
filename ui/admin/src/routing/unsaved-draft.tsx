import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useSyncExternalStore, type ReactNode } from "react";

const leaveText = "Leave this page? The changes you have not saved will be lost.";

interface DraftLedger {
  held(): boolean;
  subscribe(listener: () => void): () => void;
  hold(resource: string, unsaved: boolean): void;
  confirmLeave(): boolean;
  mayLeave(): boolean;
}

export function draftLedger(confirmLeave: (text: string) => boolean = (text) => window.confirm(text)): DraftLedger {
  const unsaved = new Set<string>();
  const listeners = new Set<() => void>();
  const notify = () => listeners.forEach(listener => listener());
  const confirmed = () => unsaved.size === 0 || confirmLeave(leaveText);
  return {
    held: () => unsaved.size > 0,
    subscribe: listener => { listeners.add(listener); return () => { listeners.delete(listener); }; },
    hold: (resource, dirty) => { const previous = unsaved.size; if (dirty) unsaved.add(resource); else unsaved.delete(resource); if (previous !== unsaved.size) notify(); },
    // Some departures, such as sign-out, can fail after the administrator
    // agrees to leave. Confirm them without releasing the guard; a successful
    // departure unmounts the resource and releases it in the ordinary way.
    confirmLeave: confirmed,
    // Nothing to lose is never worth a question. Agreeing to lose the draft is
    // the explicit discard, so the resource is released and the departure that
    // follows does not ask a second time about work already given up.
    mayLeave: () => {
      if (!confirmed()) return false;
      const hadDrafts = unsaved.size > 0;
      unsaved.clear();
      if (hadDrafts) notify();
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

const noDrafts = () => false;
const noSubscription = (_listener: () => void) => () => {};
/** Prevent reloads that would unmount another editor's unsaved draft. */
export function useHasUnsavedDrafts(): boolean {
  const ledger = useContext(LedgerContext);
  return useSyncExternalStore(ledger?.subscribe ?? noSubscription, ledger?.held ?? noDrafts, noDrafts);
}
