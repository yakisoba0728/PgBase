import { useCallback, useEffect, useState } from "react";
import { useBackend } from "../auth/session";
import { SchemaError, type OperationReceipt } from "../backend/ports";

const cursorRejectedText = "This history page is no longer available. Reload the page to start again.";

export interface OperationHistoryView {
  items: readonly OperationReceipt[];
  phase: "loading" | "ready" | "failed";
  error: string | null;
  hasMore: boolean;
  loadingMore: boolean;
  loadMore(): void;
}

interface State {
  items: OperationReceipt[];
  cursor: string | undefined;
  phase: OperationHistoryView["phase"];
  error: string | null;
  loading: boolean;
}

// A cursor is minted for one owner and one position. When the ledger refuses
// one, the pages already read stay on screen: silently emptying the list would
// claim there is no history, and silently restarting it would claim the next
// page is the first.
function pageMessage(caught: unknown): string {
  if (caught instanceof SchemaError && caught.reason === "invalid-cursor") return cursorRejectedText;
  return "Unable to load schema operations.";
}

/**
 * Reads the owner-scoped operation ledger one bounded page at a time through
 * the history endpoint. Pages are appended in the order the ledger returned
 * them, so the `(created_at DESC, operation_id DESC)` keyset the server
 * guarantees is the order the administrator sees; nothing here re-sorts, and no
 * single-status read is ever used to assemble the list.
 */
export function useOperationHistory(): OperationHistoryView {
  const { schema } = useBackend();
  const [state, setState] = useState<State>({ items: [], cursor: undefined, phase: "loading", error: null, loading: true });

  useEffect(() => {
    const controller = new AbortController();
    void schema.operations(undefined, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        setState({ items: [...page.items], cursor: page.nextCursor, phase: "ready", error: null, loading: false });
      })
      .catch((caught: unknown) => {
        if (controller.signal.aborted) return;
        setState({ items: [], cursor: undefined, phase: "failed", error: pageMessage(caught), loading: false });
      });
    return () => controller.abort();
  }, [schema]);

  const loadMore = useCallback(async () => {
    const cursor = state.cursor;
    if (cursor === undefined || state.loading) return;
    setState((current) => ({ ...current, loading: true, error: null }));
    try {
      const page = await schema.operations(cursor);
      setState((current) => ({ ...current, items: [...current.items, ...page.items], cursor: page.nextCursor, loading: false }));
    } catch (caught: unknown) {
      setState((current) => ({ ...current, cursor: undefined, loading: false, error: pageMessage(caught) }));
    }
  }, [schema, state.cursor, state.loading]);

  return {
    items: state.items,
    phase: state.phase,
    error: state.error,
    hasMore: state.cursor !== undefined,
    loadingMore: state.loading && state.phase === "ready",
    loadMore: () => void loadMore(),
  };
}
