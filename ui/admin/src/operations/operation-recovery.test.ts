import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useOperationRecovery } from "./operation-recovery";
import { SchemaError, type OperationReceipt } from "../backend/ports";
import { backendScript, callsTo, deferredStep, type BackendScript } from "../test/backend-script";
import { administrator, signedInScript, sessionWrapper } from "../test/harness";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";

const operationID = "33333333-3333-4333-8333-333333333333";

function receipt(patch: Partial<OperationReceipt> = {}): OperationReceipt {
  return {
    operationId: operationID, operation: "collection_create", collectionId: "posts-id",
    status: "running", baseVersion: 7, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:01Z", ...patch,
  };
}

interface TestRecoveryTiming {
  intervalMs: number;
  maximumDurationMs: number;
  readTimeoutMs: number;
  initialNotFoundGraceMs: number;
}

function open(
  script: BackendScript = signedInScript(),
  timing?: TestRecoveryTiming,
  inspectSignal?: (signal: AbortSignal | undefined) => void,
  firstFailure?: SchemaError,
) {
  const backend = memoryBackend(script);
  if (firstFailure !== undefined) {
    const operation = backend.schema.operation;
    let failed = false;
    backend.schema.operation = (operationId, signal) => {
      if (!failed) {
        failed = true;
        return Promise.reject(firstFailure);
      }
      return operation(operationId, signal);
    };
  }
  if (inspectSignal !== undefined) {
    const operation = backend.schema.operation;
    backend.schema.operation = (operationId, signal) => {
      inspectSignal(signal);
      return operation(operationId, signal);
    };
  }
  const wrapper = sessionWrapper(backend, memoryHistory());
  const view = renderHook(({ id }: { id: string | null }) => useOperationRecovery(id, timing), {
    initialProps: { id: operationID as string | null },
    wrapper,
  });
  return { script, ...view };
}

function compressedTiming(patch: Partial<TestRecoveryTiming> = {}): TestRecoveryTiming {
  return {
    intervalMs: 10,
    maximumDurationMs: 60,
    readTimeoutMs: 20,
    initialNotFoundGraceMs: 20,
    ...patch,
  };
}

/** Lets the immediate first read and every scheduled re-read run. */
async function settleReads(count: number, intervalMs = 1000): Promise<void> {
  if (count === 0) return;
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  for (let read = 1; read < count; read += 1) {
    await act(async () => { await vi.advanceTimersByTimeAsync(intervalMs); });
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

  it("stops at its absolute deadline and leaves an explicit re-check", async () => {
    vi.useFakeTimers();
    const timing = compressedTiming();
    const script = signedInScript();
    for (let read = 0; read < 10; read += 1) script.operation.push({ value: receipt({ status: "running" }) });
    const { result } = open(script, timing);

    await settleReads(6, timing.intervalMs);

    expect(callsTo(script, "operation")).toHaveLength(6);
    expect(result.current?.checking).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(timing.intervalMs - 1); });
    expect(callsTo(script, "operation")).toHaveLength(6);
    expect(result.current?.checking).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });

    // No read starts at the deadline and no response can move the deadline.
    expect(callsTo(script, "operation")).toHaveLength(6);
    expect(result.current?.checking).toBe(false);
    expect(result.current?.undecided).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(callsTo(script, "operation")).toHaveLength(6);

    await act(async () => { result.current?.recheck(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(callsTo(script, "operation")).toHaveLength(7);
  });

  it("does not extend the absolute deadline by response latency", async () => {
    vi.useFakeTimers();
    const timing = compressedTiming({ readTimeoutMs: 30 });
    const first = deferredStep<OperationReceipt>();
    const second = deferredStep<OperationReceipt>();
    const script = signedInScript();
    script.operation.push(first.step, second.step);
    const { result } = open(script, timing);

    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    await act(async () => { await vi.advanceTimersByTimeAsync(20); });
    await act(async () => { first.settle({ value: receipt({ status: "running" }) }); });
    await act(async () => { await vi.advanceTimersByTimeAsync(timing.intervalMs); });
    expect(callsTo(script, "operation")).toHaveLength(2);

    await act(async () => { await vi.advanceTimersByTimeAsync(20); });
    await act(async () => { second.settle({ value: receipt({ status: "running" }) }); });
    await act(async () => { await vi.advanceTimersByTimeAsync(timing.intervalMs - 1); });
    expect(result.current?.checking).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });

    expect(callsTo(script, "operation")).toHaveLength(2);
    expect(result.current?.checking).toBe(false);
  });

  it("times out a never-settling read and still stops at the absolute deadline", async () => {
    vi.useFakeTimers();
    const timing = compressedTiming();
    const script = signedInScript();
    for (let read = 0; read < 4; read += 1) script.operation.push(deferredStep<OperationReceipt>().step);
    const aborted = vi.fn();
    const { result } = open(script, timing, (signal) => signal?.addEventListener("abort", aborted));

    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(callsTo(script, "operation")).toHaveLength(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(timing.readTimeoutMs); });
    expect(result.current?.status).toBe("unreadable");
    expect(result.current?.checking).toBe(true);

    await act(async () => { await vi.advanceTimersByTimeAsync(timing.intervalMs); });
    await act(async () => { await vi.advanceTimersByTimeAsync(timing.readTimeoutMs); });
    await act(async () => { await vi.advanceTimersByTimeAsync(timing.intervalMs); });

    expect(callsTo(script, "operation")).toHaveLength(2);
    expect(aborted).toHaveBeenCalledTimes(2);
    expect(result.current?.checking).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(callsTo(script, "operation")).toHaveLength(2);
  });

  it("keeps polling after a transient unreadable response and reaches the ledger verdict", async () => {
    vi.useFakeTimers();
    const script = signedInScript();
    script.operation.push({ fault: { kind: "unreachable" } });
    script.operation.push({ value: receipt({ status: "running" }) });
    script.operation.push({ value: receipt({ status: "committed", committedVersion: 8 }) });
    const { result } = open(script);

    await settleReads(1);

    expect(result.current?.status).toBe("unreadable");
    expect(result.current?.undecided).toBe(true);
    expect(result.current?.checking).toBe(true);

    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(result.current?.status).toBe("running");
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(result.current?.status).toBe("committed");
    expect(result.current?.checking).toBe(false);
  });

  it.each(["rate_limited", "authentication_unavailable"])(
    "keeps polling after the authentication middleware returns %s",
    async (code) => {
      vi.useFakeTimers();
      const script = signedInScript();
      script.operation.push({ value: receipt({ status: "committed", committedVersion: 8 }) });
      const { result } = open(script, undefined, undefined, new SchemaError("failed", code));

      await settleReads(1);
      expect(result.current?.status).toBe("unreadable");
      expect(result.current?.checking).toBe(true);

      await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
      expect(result.current?.status).toBe("committed");
      expect(result.current?.checking).toBe(false);
    },
  );

  it("keeps recovery active through a 65-minute observation window", async () => {
    vi.useFakeTimers();
    const beganAt = Date.parse("2026-09-03T00:00:00Z");
    vi.setSystemTime(beganAt);
    const timing = {
      intervalMs: 60 * 60 * 1000,
      maximumDurationMs: 65 * 60 * 1000,
      readTimeoutMs: 10 * 1000,
      initialNotFoundGraceMs: 5 * 1000,
    };
    const script = backendScript();
    script.resume.push({
      value: {
        kind: "authenticated",
        session: {
          administrator,
          expiresAt: new Date(beganAt + 2 * 60 * 60 * 1000).toISOString(),
        },
      },
    });
    script.operation.push(
      { value: receipt({ status: "running" }) },
      { value: receipt({ status: "running" }) },
    );
    const { result } = open(script, timing);

    await settleReads(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(60 * 60 * 1000); });
    expect(result.current?.checking).toBe(true);

    await act(async () => { await vi.advanceTimersByTimeAsync(5 * 60 * 1000 - 1); });
    expect(result.current?.checking).toBe(true);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(result.current?.checking).toBe(false);
    expect(callsTo(script, "operation")).toHaveLength(2);
  });

  it("bounds the initial not-found race instead of polling a missing operation for the full window", async () => {
    vi.useFakeTimers();
    const timing = compressedTiming({ initialNotFoundGraceMs: 25, maximumDurationMs: 100 });
    const script = signedInScript();
    for (let read = 0; read < 8; read += 1) script.operation.push({ fault: { kind: "not-found" } });
    const { result } = open(script, timing);

    await settleReads(4, timing.intervalMs);

    expect(callsTo(script, "operation")).toHaveLength(4);
    expect(result.current?.status).toBe("unreadable");
    expect(result.current?.checking).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(100); });
    expect(callsTo(script, "operation")).toHaveLength(4);
  });

  it("stops immediately on a permanent typed operation failure", async () => {
    vi.useFakeTimers();
    const script = signedInScript();
    script.operation.push({ fault: { kind: "operation-conflict" } });
    const { result } = open(script);

    await settleReads(1);

    expect(result.current?.status).toBe("unreadable");
    expect(result.current?.checking).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(callsTo(script, "operation")).toHaveLength(1);
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
