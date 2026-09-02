import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useOperationHistory, type OperationHistoryView } from "./operation-history";
import type { OperationReceipt } from "../backend/ports";
import { callsTo, type BackendScript } from "../test/backend-script";
import { signedInScript, sessionWrapper } from "../test/harness";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";

function receipt(operationId: string, patch: Partial<OperationReceipt> = {}): OperationReceipt {
  return {
    operationId, operation: "collection_create", collectionId: "posts-id", status: "committed",
    baseVersion: 7, committedVersion: 8, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:01Z", ...patch,
  };
}

function open(script: BackendScript) {
  const wrapper = sessionWrapper(memoryBackend(script), memoryHistory());
  return renderHook(() => useOperationHistory(), { wrapper });
}

const ids = (view: OperationHistoryView) => view.items.map((item) => item.operationId);

describe("schema operation history", () => {
  it("reads one bounded page without asking for a cursor first", async () => {
    const script = signedInScript();
    script.operations.push({ value: { items: [receipt("op-1")], nextCursor: "page-2" } });
    const { result } = open(script);

    await waitFor(() => expect(result.current.phase).toBe("ready"));
    expect(callsTo(script, "operations")[0].detail).toEqual({});
    expect(result.current.hasMore).toBe(true);
  });

  it("keeps the ledger's own ordering across pages instead of re-sorting them", async () => {
    const script = signedInScript();
    // Every receipt shares one creation instant, so only the ledger's
    // (created_at DESC, operation_id DESC) tie-break decides this order. A view
    // that sorted by any field it can see would not reproduce it.
    script.operations.push({ value: { items: [receipt("op-d"), receipt("op-b")], nextCursor: "page-2" } });
    script.operations.push({ value: { items: [receipt("op-c"), receipt("op-a")] } });
    const { result } = open(script);

    await waitFor(() => expect(result.current.phase).toBe("ready"));
    await act(async () => { result.current.loadMore(); });
    await waitFor(() => expect(result.current.items).toHaveLength(4));

    expect(ids(result.current)).toEqual(["op-d", "op-b", "op-c", "op-a"]);
    expect(callsTo(script, "operations")[1].detail).toEqual({ cursor: "page-2" });
    expect(result.current.hasMore).toBe(false);
  });

  it("reports a cursor the ledger refuses without emptying or restarting the history", async () => {
    const script = signedInScript();
    script.operations.push({ value: { items: [receipt("op-1")], nextCursor: "another-owners-cursor" } });
    script.operations.push({ fault: { kind: "invalid-cursor" } });
    const { result } = open(script);

    await waitFor(() => expect(result.current.phase).toBe("ready"));
    await act(async () => { result.current.loadMore(); });
    await waitFor(() => expect(result.current.error).not.toBeNull());

    // Emptying the list would claim there is no history and restarting it would
    // claim the next page is the first; the pages already read stay put.
    expect(ids(result.current)).toEqual(["op-1"]);
    expect(result.current.hasMore).toBe(false);
    expect(result.current.error).toContain("no longer available");
  });

  it("reports an unreadable first page as a failure rather than an empty history", async () => {
    const script = signedInScript();
    script.operations.push({ fault: { kind: "unreachable" } });
    const { result } = open(script);

    await waitFor(() => expect(result.current.phase).toBe("failed"));
    expect(result.current.error).toBe("Unable to load schema operations.");
    expect(result.current.items).toHaveLength(0);
  });

  it("never asks for another page once the ledger stopped issuing a cursor", async () => {
    const script = signedInScript();
    script.operations.push({ value: { items: [receipt("op-1")] } });
    const { result } = open(script);

    await waitFor(() => expect(result.current.phase).toBe("ready"));
    expect(result.current.hasMore).toBe(false);
    await act(async () => { result.current.loadMore(); });

    expect(callsTo(script, "operations")).toHaveLength(1);
  });
});
