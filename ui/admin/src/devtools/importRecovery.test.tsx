import { createMemoryRouter, RouterProvider } from "react-router";
import { act, fireEvent, render, screen, waitFor, within, cleanup } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import { setLanguage } from "../i18n";
import { Undecided } from "../schema/apply";
import { SchemaImport } from "./SchemaImport";
import { readPending, writePending, type PendingImport } from "./model";
import { runImport } from "./queries";

const api = vi.hoisted(() => ({ apply: vi.fn(), status: vi.fn(), preview: vi.fn() }));
vi.mock("../api/schema", async (original) => ({
  ...(await original<typeof import("../api/schema")>()),
  applySchemaImport: api.apply,
  previewSchemaImport: api.preview,
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
  render(<QueryClientProvider client={client}><RouterProvider router={createMemoryRouter([{ path: "*", element: <SchemaImport /> }])} /></QueryClientProvider>);
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

  it("preserves a restored identity when a resend is definitively refused", async () => {
    api.apply.mockRejectedValueOnce(new ApiError(400, "invalid_request", "rejected"));
    restore();
    await waitFor(() => expect(api.apply).toHaveBeenCalledOnce());
    await waitFor(() => expect(readPending()).toEqual(pending));
  });

  it("rejects a failed receipt instead of reporting success", async () => {
    api.apply.mockResolvedValueOnce({ operationId: pending.identity.operationId, status: "failed", failureCode: "rejected" });
    await expect(runImport(pending)).rejects.toMatchObject({ code: "schema_operation_failed" });
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

it("applies a valid metadata-only preview with no physical sections", async () => {
  api.preview.mockResolvedValue({ document: pending.document, changes: [], checksum: "c", policyChecksum: "p", baseVersion: 1 });
  api.apply.mockResolvedValue({ operationId: pending.identity.operationId, status: "committed", version: 2 });
  render(<QueryClientProvider client={client}><RouterProvider router={createMemoryRouter([{ path: "*", element: <SchemaImport /> }])} /></QueryClientProvider>);
  fireEvent.change(screen.getByRole("textbox", { name: "Document to import" }), { target: { value: '{"format":"pgbase.schema/v1","collections":[]}' } });
  fireEvent.click(screen.getByRole("button", { name: "Preview" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
  await waitFor(() => expect(api.apply).toHaveBeenCalled());
});
it("does not let a delayed file read overwrite newer typed JSON", async () => {
  let finish!: (value: string) => void;
  render(<QueryClientProvider client={client}><RouterProvider router={createMemoryRouter([{ path: "*", element: <SchemaImport /> }])} /></QueryClientProvider>);
  fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [{ text: () => new Promise<string>((resolve) => { finish = resolve; }) }] } });
  const editor = screen.getByRole("textbox", { name: "Document to import" });
  fireEvent.change(editor, { target: { value: "newer JSON" } });
  await act(async () => { finish("old file"); });
  expect(editor).toHaveValue("newer JSON");
});
it("confirms an import that loses data by typing the affected collection name, not a fixed word", async () => {
  const definition = { name: "posts", fields: [], indexes: [] };
  api.preview.mockResolvedValue({ document: pending.document, changes: [{ definition, sql: ["DROP TABLE app.posts"], checksum: "c", baseVersion: 1 }], checksum: "c", policyChecksum: "p", baseVersion: 1 });
  api.apply.mockResolvedValue({ operationId: pending.identity.operationId, status: "committed", version: 2 });
  render(<QueryClientProvider client={client}><RouterProvider router={createMemoryRouter([{ path: "*", element: <SchemaImport /> }])} /></QueryClientProvider>);
  fireEvent.change(screen.getByRole("textbox", { name: "Document to import" }), { target: { value: '{"format":"pgbase.schema/v1","collections":[]}' } });
  fireEvent.click(screen.getByRole("button", { name: "Preview" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
  const dialog = await screen.findByRole("dialog");
  const phrase = within(dialog).getByRole("textbox", { name: "Type posts to confirm" });
  const confirm = within(dialog).getByRole("button", { name: "Apply" });
  fireEvent.change(phrase, { target: { value: "delete" } });
  expect(confirm).toBeDisabled();
  fireEvent.change(phrase, { target: { value: "posts" } });
  fireEvent.click(confirm);
  await waitFor(() => expect(api.apply).toHaveBeenCalled());
});
it("confirms an import that loses data in several collections by typing the first one's name and lists them all", async () => {
  const change = (name: string) => ({ definition: { name, fields: [], indexes: [] }, sql: [`DROP TABLE app.${name}`], checksum: "c", baseVersion: 1 });
  api.preview.mockResolvedValue({ document: pending.document, changes: [change("posts"), change("tags")], checksum: "c", policyChecksum: "p", baseVersion: 1 });
  api.apply.mockResolvedValue({ operationId: pending.identity.operationId, status: "committed", version: 2 });
  render(<QueryClientProvider client={client}><RouterProvider router={createMemoryRouter([{ path: "*", element: <SchemaImport /> }])} /></QueryClientProvider>);
  fireEvent.change(screen.getByRole("textbox", { name: "Document to import" }), { target: { value: '{"format":"pgbase.schema/v1","collections":[]}' } });
  fireEvent.click(screen.getByRole("button", { name: "Preview" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Apply" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Apply" }));
  const dialog = await screen.findByRole("dialog");
  expect(within(dialog).getByText("posts")).toBeInTheDocument();
  expect(within(dialog).getByText("tags")).toBeInTheDocument();
  const phrase = within(dialog).getByRole("textbox", { name: "Type posts to confirm" });
  const confirm = within(dialog).getByRole("button", { name: "Apply" });
  fireEvent.change(phrase, { target: { value: "posts, tags" } });
  expect(confirm).toBeDisabled();
  fireEvent.change(phrase, { target: { value: "posts" } });
  fireEvent.click(confirm);
  await waitFor(() => expect(api.apply).toHaveBeenCalled());
});
