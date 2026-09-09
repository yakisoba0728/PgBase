import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SQLConsolePage } from "./sql-console-page";
import type { SQLConsoleExecution, SQLConsolePort } from "../backend/sql-console-backend";

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
    history: vi.fn(async () => ({ executions: [], writeEnabled: true, writeTargets: [{ schema: "extension", table: "ledger_entries", columns: ["id", "amount"] }] })),
    ...overrides,
  };
}

afterEach(() => vi.useRealTimers());

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
