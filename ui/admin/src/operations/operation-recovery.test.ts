import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useOperationRecovery } from "./operation-recovery";
import type { OperationReceipt } from "../backend/ports";
import { backendScript, callsTo, type BackendScript } from "../test/backend-script";
import { signedInScript, sessionWrapper } from "../test/harness";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";

const operationID = "33333333-3333-4333-8333-333333333333";

function receipt(patch: Partial<OperationReceipt> = {}): OperationReceipt {
  return {
    operationId: operationID, operation: "collection_create", collectionId: "posts-id",
    status: "running", baseVersion: 7, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:01Z", ...patch,
  };
}

function open(script: BackendScript = signedInScript()) {
  const wrapper = sessionWrapper(memoryBackend(script), memoryHistory());
  const view = renderHook(({ id }: { id: string | null }) => useOperationRecovery(id), {
    initialProps: { id: operationID as string | null },
    wrapper,
  });
  return { script, ...view };
}

/** Lets the immediate first read and every scheduled re-read run. */
async function settleReads(count: number): Promise<void> {
  for (let read = 0; read < count; read += 1) {
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  }
}

afterEach(() => vi.useRealTimers());

describe("schema operation recovery", () => {
  it("recovers a DDL that outlived the request and then committed as a success", async () => {
    vi.useFakeTimers();
    const script = signedInScript();
    // The apply already answered "not decidable"; the ledger is still running
    // when the first status read lands, exactly as a >30s DDL behaves.
    script.operation.push({ value: receipt({ status: "running" }) });
    script.operation.push({ value: receipt({ status: "running" }) });
    script.operation.push({ value: receipt({ status: "committed", committedVersion: 8 }) });
    const { result } = open(script);

    await settleReads(3);

    expect(result.current?.status).toBe("committed");
    expect(result.current?.undecided).toBe(false);
    expect(callsTo(script, "operation")).toHaveLength(3);
  });

  it.each(["running", "unknown"] as const)("never settles a %s operation into a success or a failure", async (status) => {
    vi.useFakeTimers();
    const script = signedInScript();
    for (let read = 0; read < 4; read += 1) script.operation.push({ value: receipt({ status }) });
    const { result } = open(script);

    await settleReads(4);

    expect(result.current?.status).toBe(status);
    expect(result.current?.undecided).toBe(true);
  });

  it("stops reading after its budget and leaves an explicit re-check", async () => {
    vi.useFakeTimers();
    const script = signedInScript();
    for (let read = 0; read < 40; read += 1) script.operation.push({ value: receipt({ status: "running" }) });
    const { result } = open(script);

    await settleReads(40);

    // A transaction that outlives any plausible DDL must not poll for ever.
    expect(callsTo(script, "operation")).toHaveLength(30);
    expect(result.current?.checking).toBe(false);
    expect(result.current?.undecided).toBe(true);

    await act(async () => { result.current?.recheck(); });
    await settleReads(1);
    expect(callsTo(script, "operation")).toHaveLength(31);
  });

  it("reports a status it could not read as unreadable rather than as a verdict", async () => {
    vi.useFakeTimers();
    const script = signedInScript();
    script.operation.push({ fault: { kind: "unreachable" } });
    const { result } = open(script);

    await settleReads(1);

    expect(result.current?.status).toBe("unreadable");
    expect(result.current?.undecided).toBe(true);
    expect(result.current?.checking).toBe(false);
  });

  it("carries the ledger's bounded failure classification when the operation failed", async () => {
    vi.useFakeTimers();
    const script = signedInScript();
    script.operation.push({ value: receipt({ status: "failed", failureCode: "apply_failed" }) });
    const { result } = open(script);

    await settleReads(1);

    expect(result.current).toMatchObject({ status: "failed", failureCode: "apply_failed", undecided: false });
  });

  it("reads nothing until an apply has an identity to recover", async () => {
    const script = backendScript();
    script.resume.push({ value: { kind: "authenticated", session: { administrator: { id: "a", email: "a@b.test", createdAt: "t" }, expiresAt: "2026-09-02T00:00:00Z" } } });
    const wrapper = sessionWrapper(memoryBackend(script), memoryHistory());
    const { result } = renderHook(() => useOperationRecovery(null), { wrapper });

    await waitFor(() => expect(result.current).toBeNull());
    expect(callsTo(script, "operation")).toHaveLength(0);
  });
});
