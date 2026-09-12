import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SQLConsolePage } from "./sql-console-page";
import { SQLConsoleError, type SQLConsoleExecution, type SQLConsolePort } from "../backend/sql-console-backend";

const id = "8bff0101-0000-4000-8000-000000000001";
function sample(patch: Partial<SQLConsoleExecution> = {}): SQLConsoleExecution {
  return {
    id, mode: "read", dryRun: false, status: "succeeded", createdAt: "2026-09-10T00:00:00Z",
    result: { rows: 1, affectedRows: 0, bytes: 48, truncated: false, dryRun: false, elapsedMs: 4, statements: [{ affectedRows: 0, columns: [{ name: "value", typeOid: 20, typeName: "int8" }], rows: [[{ kind: "integer", text: "9223372036854775807" }, { kind: "null" }]] }] },
    ...patch,
  };
}
function port(overrides: Partial<SQLConsolePort> = {}): SQLConsolePort {
  return {
    submit: vi.fn(async () => sample()), execution: vi.fn(async () => sample()), cancel: vi.fn(async () => sample({ status: "canceled", result: undefined })),
    history: vi.fn(async () => ({ executions: [], writeEnabled: true, writeTargets: [{ schema: "extension", table: "ledger_entries", columns: ["id", "amount"] }], sessionIdentity: "session-a" })),
    ...overrides,
  };
}

afterEach(() => { vi.useRealTimers(); window.sessionStorage.clear(); });

describe("SQL console page", () => {
  it("starts read-only and retains exact tagged values instead of coercing them to JavaScript numbers", async () => {
    const backend = port();
    vi.stubGlobal("crypto", { randomUUID: vi.fn(() => id) });
    render(<SQLConsolePage port={backend} />);

    expect(await screen.findByText("extension.ledger_entries")).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "Run read query" }));
    expect((backend.submit as ReturnType<typeof vi.fn>).mock.calls[0][0]).toMatchObject({ operationID: id, mode: "read", dryRun: false });
    expect(await screen.findByText("9223372036854775807")).toBeVisible();
    expect(screen.getByText("NULL", { selector: "td span" })).toBeVisible();
    expect(document.querySelector(".sql-console-results.table-scroll")).toBeInTheDocument();
  });

  it("requires an explicit confirmation for a configured transactional write and sends one idempotency key", async () => {
    const backend = port();
    vi.stubGlobal("crypto", { randomUUID: vi.fn().mockReturnValueOnce("operation-id").mockReturnValueOnce("write-key") });
    render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");

    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));
    expect(await screen.findByText("Confirm the transactional write before executing it.")).toBeVisible();
    expect(backend.submit).not.toHaveBeenCalled();
    await userEvent.click(screen.getByLabelText("I confirm this allowed write transaction should execute once"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));

    expect(backend.submit).toHaveBeenCalledWith(expect.objectContaining({ operationID: "operation-id", idempotencyKey: "write-key", mode: "write", dryRun: false }));
  });

  it("does not send a transactional write when its recovery identity cannot persist", async () => {
    const backend = port();
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("storage denied");
    });
    vi.stubGlobal("crypto", {
      randomUUID: vi.fn().mockReturnValueOnce("write-operation-id").mockReturnValueOnce("write-idempotency-key"),
    });
    render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByLabelText("I confirm this allowed write transaction should execute once"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));

    expect(await screen.findByText(/cannot retain recovery information/)).toBeVisible();
    expect(backend.submit).not.toHaveBeenCalled();
    set.mockRestore();
  });

  it("keeps read mode to one statement and discards extra write drafts when returning to read", async () => {
    const backend = port();
    render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    expect(screen.queryByRole("button", { name: "Add statement" })).not.toBeInTheDocument();

    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByRole("button", { name: "Add statement" }));
    expect(screen.getByLabelText("SQL statement 2")).toBeVisible();
    await userEvent.click(screen.getByLabelText("Read only"));

    expect(screen.queryByLabelText("SQL statement 2")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add statement" })).not.toBeInTheDocument();
  });

  it("reconciles a lost write submission with its retained identity before another write can be sent", async () => {
    const backend = port({ submit: vi.fn(async () => { throw new Error("response lost"); }), execution: vi.fn(async () => sample({ mode: "write", status: "succeeded" })) });
    vi.stubGlobal("crypto", { randomUUID: vi.fn().mockReturnValueOnce("write-operation-id").mockReturnValueOnce("write-idempotency-key") });
    render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByLabelText("I confirm this allowed write transaction should execute once"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));

    expect(await screen.findByRole("button", { name: "Recheck write execution" })).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "Recheck write execution" }));

    expect(backend.submit).toHaveBeenCalledTimes(1);
    expect(backend.submit).toHaveBeenCalledWith(expect.objectContaining({ operationID: "write-operation-id", idempotencyKey: "write-idempotency-key", mode: "write" }));
    expect(backend.execution).toHaveBeenCalledWith("write-operation-id");
  });

  it("replays a definitely absent write with the same identity and body after a reload", async () => {
    const submitted = vi.fn().mockRejectedValueOnce(new Error("response lost")).mockResolvedValueOnce(sample({ mode: "write", status: "succeeded" }));
    const backend = port({ submit: submitted, execution: vi.fn(async () => { throw new SQLConsoleError(404, "not_found"); }) });
    vi.stubGlobal("crypto", { randomUUID: vi.fn().mockReturnValueOnce("write-operation-id").mockReturnValueOnce("write-idempotency-key") });
    const first = render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByLabelText("I confirm this allowed write transaction should execute once"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));
    await screen.findByRole("button", { name: "Recheck write execution" });
    first.unmount();

    render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByRole("button", { name: "Recheck write execution" }));

    expect(backend.execution).toHaveBeenCalledWith("write-operation-id");
    expect(submitted).toHaveBeenCalledTimes(2);
    expect(submitted.mock.calls[1][0]).toEqual(submitted.mock.calls[0][0]);
  });

  it("keeps an unknown write retained across a browser restart and never replays it", async () => {
    const unknown = sample({ mode: "write", status: "unknown", result: { ...sample().result!, committed: undefined }, failure: { code: "commit_unknown", message: "Commit response was lost" } });
    const submitted = vi.fn(async () => unknown);
    const backend = port({ submit: submitted, execution: vi.fn(async () => unknown) });
    vi.stubGlobal("crypto", { randomUUID: vi.fn().mockReturnValueOnce("write-operation-id").mockReturnValueOnce("write-idempotency-key") });
    const first = render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByLabelText("I confirm this allowed write transaction should execute once"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));
    expect(await screen.findByText(/could not determine the final commit state/)).toBeVisible();
    first.unmount();

    render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByRole("button", { name: "Recheck write execution" }));

    expect(backend.execution).toHaveBeenCalledWith("write-operation-id");
    expect(submitted).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Recheck write execution" })).toBeVisible();
  });

  it("never exposes a retained write across a different or expired administrator session scope", async () => {
    const submitted = vi.fn(async () => { throw new Error("response lost"); });
    const firstBackend = port({ submit: submitted });
    vi.stubGlobal("crypto", { randomUUID: vi.fn().mockReturnValueOnce("write-operation-id").mockReturnValueOnce("write-idempotency-key") });
    const first = render(<SQLConsolePage port={firstBackend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByLabelText("I confirm this allowed write transaction should execute once"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));
    await screen.findByRole("button", { name: "Recheck write execution" });
    first.unmount();

    for (const scope of ["session-b", "session-a-after-expiry"]) {
      const otherBackend = port({ history: vi.fn(async () => ({ executions: [], writeEnabled: true, writeTargets: [], sessionIdentity: scope })) });
      const other = render(<SQLConsolePage port={otherBackend} />);
      await screen.findByRole("button", { name: "Run read query" });
      await userEvent.click(screen.getByLabelText("Transactional write"));
      expect(screen.queryByRole("button", { name: "Recheck write execution" })).not.toBeInTheDocument();
      expect(otherBackend.execution).not.toHaveBeenCalled();
      other.unmount();
    }
  });

  it("removes the legacy unscoped recovery cache instead of reading it", async () => {
    window.sessionStorage.setItem("pgbase.sql-console.pending-write/v1", JSON.stringify({ operationID: "old", idempotencyKey: "old", mode: "write", dryRun: false, statements: [{ sql: "DELETE FROM extension.items" }] }));
    const backend = port({ history: vi.fn(async () => ({ executions: [], writeEnabled: true, writeTargets: [] })) });
    render(<SQLConsolePage port={backend} />);
    await screen.findByRole("button", { name: "Run read query" });

    expect(window.sessionStorage.getItem("pgbase.sql-console.pending-write/v1")).toBeNull();
    await userEvent.click(screen.getByLabelText("Transactional write"));
    expect(screen.queryByRole("button", { name: "Recheck write execution" })).not.toBeInTheDocument();
  });

  it("releases a definite write rejection instead of treating it as an uncertain execution", async () => {
    const backend = port({ submit: vi.fn(async () => { throw new SQLConsoleError(400, "invalid_request"); }) });
    vi.stubGlobal("crypto", { randomUUID: vi.fn(() => id) });
    render(<SQLConsolePage port={backend} />);
    await screen.findByText("extension.ledger_entries");
    await userEvent.click(screen.getByLabelText("Transactional write"));
    await userEvent.click(screen.getByLabelText("I confirm this allowed write transaction should execute once"));
    await userEvent.click(screen.getByRole("button", { name: "Execute write" }));

    expect(await screen.findByText("The SQL execution request was rejected.")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Recheck write execution" })).not.toBeInTheDocument();
  });

  it("re-fetches an inspected durable receipt while its bounded result is still available", async () => {
    const historyExecution = sample({ result: undefined, resultUntil: "2099-01-01T00:00:00Z" });
    const backend = port({ history: vi.fn(async () => ({ executions: [historyExecution], writeEnabled: false, writeTargets: [] })), execution: vi.fn(async () => sample()) });
    render(<SQLConsolePage port={backend} />);
    expect(await screen.findByRole("button", { name: "Inspect" })).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "Inspect" }));

    expect(backend.execution).toHaveBeenCalledWith(id);
    expect(await screen.findByText("9223372036854775807")).toBeVisible();
  });

  it.each([
    ["2000-01-01T00:00:00Z", "The bounded result expired"],
    ["2099-01-01T00:00:00Z", "The bounded result is unavailable"],
  ])("labels a durable summary with omitted statement rows as unavailable (%s)", async (resultUntil, expected) => {
    const full = sample().result!;
    const durable = sample({ resultUntil, result: { ...full, statements: undefined, committed: true } });
    const backend = port({ history: vi.fn(async () => ({ executions: [durable], writeEnabled: false, writeTargets: [], sessionIdentity: "session-a" })), execution: vi.fn(async () => durable) });
    render(<SQLConsolePage port={backend} />);
    await userEvent.click(await screen.findByRole("button", { name: "Inspect" }));

    expect(await screen.findByText(new RegExp(expected))).toBeVisible();
    expect(screen.getByText(/1 rows, 0 affected rows, 48 bytes/)).toBeVisible();
    expect(screen.getByText(/Committed/)).toBeVisible();
  });

  it("omits an untouched timeout and validates an edited timeout against the server capability", async () => {
    const backend = port({ history: vi.fn(async () => ({ executions: [], writeEnabled: true, writeTargets: [], defaultTimeoutMs: 1500, maximumTimeoutMs: 2000 })) });
    vi.stubGlobal("crypto", { randomUUID: vi.fn(() => id) });
    render(<SQLConsolePage port={backend} />);
    await screen.findByText(/Server default: 1500 ms/);
    await userEvent.click(screen.getByRole("button", { name: "Run read query" }));
    expect((backend.submit as ReturnType<typeof vi.fn>).mock.calls[0][0]).not.toHaveProperty("timeoutMS");

    await userEvent.clear(screen.getByLabelText("Timeout in milliseconds"));
    await userEvent.type(screen.getByLabelText("Timeout in milliseconds"), "2001");
    await userEvent.click(screen.getByRole("button", { name: "Run read query" }));
    expect(await screen.findByText("Timeout must be a whole number from 1 to 2000 ms.")).toBeVisible();
    expect(backend.submit).toHaveBeenCalledTimes(1);
  });

  it("cancels a running execution and polls until the server gives its final state", async () => {
    vi.useFakeTimers();
    const running = sample({ status: "running", result: undefined });
    const backend = port({ submit: vi.fn(async () => running), cancel: vi.fn(async () => running), execution: vi.fn(async () => sample({ status: "canceled", result: undefined })) });
    vi.stubGlobal("crypto", { randomUUID: vi.fn(() => id) });
    render(<SQLConsolePage port={backend} />);
    await act(async () => { await Promise.resolve(); });
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Run read query" })); await Promise.resolve(); });
    expect(screen.getByRole("button", { name: "Cancel execution" })).toBeVisible();
    await act(async () => { fireEvent.click(screen.getByRole("button", { name: "Cancel execution" })); await Promise.resolve(); });
    await act(async () => { await vi.advanceTimersByTimeAsync(750); });

    expect(backend.cancel).toHaveBeenCalledWith(id);
    expect(backend.execution).toHaveBeenCalledWith(id, expect.any(AbortSignal));
    expect(screen.getByText("Canceled")).toBeVisible();
  });

  it("makes statement zero and source position visible, and leaves unknown outcome undecided", async () => {
    const backend = port({ submit: vi.fn(async () => sample({ status: "unknown", result: undefined, failure: { code: "commit_unknown", message: "Commit response was lost", statementIndex: 0, position: 17, sqlState: "08006" } })) });
    vi.stubGlobal("crypto", { randomUUID: vi.fn(() => id) });
    render(<SQLConsolePage port={backend} />);
    await userEvent.click(screen.getByRole("button", { name: "Run read query" }));

    expect(await screen.findByText(/Commit response was lost Statement 1. Character 17. SQLSTATE 08006/)).toBeVisible();
    expect(screen.getByText(/could not determine the final commit state/)).toBeVisible();
  });
});
