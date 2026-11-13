import type { ReactNode } from "react";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Collection } from "../api/collections";
import { ApiError } from "../api/http";
import * as records from "../api/records";
import { sessionQuery } from "../app/queries";
import { useRecordForm } from "./useRecordForm";

const collection: Collection = { id: "c1", name: "notes", tableName: "notes", fields: [], createdAt: "" };
const created = { record: { id: "r1" }, etag: '"v1"' };

afterEach(() => vi.restoreAllMocks());

describe("create retries", () => {
  it.each([
    new ApiError(0, "network_error", "lost connection"),
    new ApiError(408, "timeout", "timed out"),
    new ApiError(429, "rate_limited", "resend refused"),
    new ApiError(500, "internal_error", "unknown outcome"),
    new ApiError(502, "invalid_response", "gateway error"),
    new ApiError(504, "invalid_response", "gateway timeout"),
    new ApiError(409, "idempotency_in_progress", "still running"),
    new ApiError(409, "idempotency_conflict", "different retry payload"),
    new ApiError(201, "invalid_response", "unreadable accepted response"),
    new TypeError("response body connection lost"),
  ])("keeps the same key after an uncertain response: %s", async (error) => {
    const create = vi.spyOn(records, "createRecord").mockRejectedValueOnce(error).mockResolvedValueOnce(created);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(sessionQuery.queryKey, { administrator: { id: "admin", email: "admin@example.test", active: true, createdAt: "", updatedAt: "" }, csrfToken: "csrf", expiresAt: "", sqlConsoleEnabled: false });
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result, unmount } = renderHook(() => useRecordForm({ collection, onCreated: vi.fn(), onDeleted: vi.fn() }), { wrapper });
    await act(async () => { await result.current.save.mutateAsync().catch(() => undefined); });
    await act(async () => { await result.current.save.mutateAsync(); });
    expect(create.mock.calls[0][2]?.idempotencyKey).toBeTruthy();
    expect(create.mock.calls[1][2]?.idempotencyKey).toBe(create.mock.calls[0][2]?.idempotencyKey);
    unmount();
    client.clear();
  });

  it("uses a new key after a definite validation refusal", async () => {
    const create = vi.spyOn(records, "createRecord").mockRejectedValueOnce(new ApiError(422, "validation_failed", "invalid input")).mockResolvedValueOnce(created);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(sessionQuery.queryKey, { administrator: { id: "admin", email: "admin@example.test", active: true, createdAt: "", updatedAt: "" }, csrfToken: "csrf", expiresAt: "", sqlConsoleEnabled: false });
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result, unmount } = renderHook(() => useRecordForm({ collection, onCreated: vi.fn(), onDeleted: vi.fn() }), { wrapper });
    await act(async () => { await result.current.save.mutateAsync().catch(() => undefined); });
    await act(async () => { await result.current.save.mutateAsync(); });
    expect(create.mock.calls[1][2]?.idempotencyKey).not.toBe(create.mock.calls[0][2]?.idempotencyKey);
    unmount();
    client.clear();
  });
});

it("preserves every same-tab 412 edit when persistent storage is blocked", async () => {
  const fields: Collection["fields"] = ["title", "api_token"].map((name) => ({ id: name, name, type: "text", columnName: name, required: false, unique: false, indexed: false, ordinal: 0, hidden: false, presentable: false }));
  const edited = { ...collection, fields };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(sessionQuery.queryKey, { administrator: { id: "admin", email: "a", active: true, createdAt: "", updatedAt: "" }, csrfToken: "csrf", expiresAt: "", sqlConsoleEnabled: false });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  const blocked = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("quota"); });
  const first = renderHook(() => useRecordForm({ collection: edited, loaded: { record: { id: "r", title: "old" }, etag: '"1"' }, onCreated: vi.fn(), onDeleted: vi.fn() }), { wrapper });
  act(() => { first.result.current.setDraft("title", "mine"); first.result.current.setDraft("api_token", "secret"); });
  act(() => first.result.current.reload(true));
  first.unmount();
  const second = renderHook(() => useRecordForm({ collection: edited, loaded: { record: { id: "r", title: "remote" }, etag: '"2"' }, onCreated: vi.fn(), onDeleted: vi.fn() }), { wrapper });
  expect(second.result.current.drafts).toMatchObject({ title: "mine", api_token: "secret" });
  expect(blocked.mock.calls.some(([, value]) => value.includes("secret"))).toBe(false);
  second.unmount(); client.clear();
});
