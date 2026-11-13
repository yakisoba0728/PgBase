import { fireEvent, render, screen, waitFor, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import { setLanguage } from "../i18n";
import { Undecided } from "../schema/apply";
import { SchemaImport } from "./SchemaImport";
import { readPending, writePending, type PendingImport } from "./model";
import { runImport } from "./queries";

const api = vi.hoisted(() => ({ apply: vi.fn(), status: vi.fn() }));
vi.mock("../api/schema", async (original) => ({
  ...(await original<typeof import("../api/schema")>()),
  applySchemaImport: api.apply,
  getSchemaImportOperation: api.status,
}));
vi.mock("../app/operations", () => ({ trackOperation: vi.fn(), untrackOperation: vi.fn(), watchOperation: () => () => {} }));
vi.mock("../collections/queries", () => ({ collectionsQuery: { queryKey: ["collections"], queryFn: async () => [] } }));

const pending: PendingImport = {
  identity: { operationId: "00000000-0000-4000-8000-000000000001", idempotencyKey: "import-recovery-test-key" },
  document: { format: "pgbase.schema/v1", collections: [] },
  baseVersion: 1,
  checksum: "preview-checksum",
};
let client: QueryClient;
beforeEach(() => {
  vi.clearAllMocks();
  sessionStorage.clear();
  setLanguage("en");
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
});
afterEach(() => { cleanup(); client.clear(); vi.useRealTimers(); });

function restore() {
  writePending(pending);
  render(<QueryClientProvider client={client}><SchemaImport /></QueryClientProvider>);
}

describe("schema import recovery", () => {
  it.each([
    new ApiError(502, "invalid_response", "proxy"),
    new ApiError(429, "busy", "resend refused"),
    new ApiError(409, "idempotency_in_progress", "still running"),
    new ApiError(408, "invalid_response", "timeout"),
    new ApiError(200, "invalid_response", "truncated JSON"),
    new TypeError("response body read failed"),
  ])("keeps the identity after an uncertain response: %s", async (error) => {
    api.apply.mockRejectedValueOnce(error).mockResolvedValueOnce({ operationId: pending.identity.operationId, status: "committed", version: 2 });
    restore();
    await screen.findByRole("button", { name: /Check again/i });
    expect(readPending()).toEqual(pending);
    fireEvent.click(screen.getByRole("button", { name: /Check again/i }));
    await waitFor(() => expect(api.apply).toHaveBeenCalledTimes(2));
    expect(api.apply.mock.calls[1]).toEqual(api.apply.mock.calls[0]);
    await waitFor(() => expect(readPending()).toBeNull());
  });

  it("clears the identity after a definite apply rejection", async () => {
    api.apply.mockRejectedValueOnce(new ApiError(400, "invalid_request", "rejected"));
    restore();
    await waitFor(() => expect(api.apply).toHaveBeenCalledOnce());
    await waitFor(() => expect(readPending()).toBeNull());
  });

  it("keeps an accepted apply undecided when its status read fails", async () => {
    vi.useFakeTimers();
    api.apply.mockResolvedValueOnce({ operationId: pending.identity.operationId, status: "running" });
    api.status.mockRejectedValueOnce(new ApiError(404, "not_found", "status unavailable"));
    const outcome = runImport(pending).catch((error: unknown) => error);
    await vi.advanceTimersByTimeAsync(1000);
    const error = await outcome;
    expect(error).toBeInstanceOf(Undecided);
    expect(error).toMatchObject({ operationId: pending.identity.operationId });
  });
});
