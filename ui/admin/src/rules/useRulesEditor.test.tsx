import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Collection, CollectionRules } from "../api/collections";
import { useRulesEditor } from "./useRulesEditor";

const before: CollectionRules = { list: { op: "deny" }, view: { op: "allow" }, create: { op: "deny" }, update: { op: "owner" }, delete: { op: "deny" } };
const api = vi.hoisted(() => ({ getCollectionRules: vi.fn(), setCollectionRules: vi.fn(), setCollectionUserAccess: vi.fn(), previewCollectionRule: vi.fn(), listRecords: vi.fn() }));
vi.mock("../api/collections", () => ({
  getCollectionRules: api.getCollectionRules,
  setCollectionRules: api.setCollectionRules,
  getCollectionUserAccess: async () => ({ enabled: true }),
  setCollectionUserAccess: api.setCollectionUserAccess,
  previewCollectionRule: api.previewCollectionRule,
}));

vi.mock("../api/records", () => ({ listRecords: api.listRecords }));

function editor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return renderHook(() => useRulesEditor({ name: "orders", type: "base", fields: [] } as unknown as Collection, "/collections/orders"), { wrapper });
}

beforeEach(() => {
  vi.clearAllMocks();
  api.getCollectionRules.mockResolvedValue({ collection: "orders", rules: before });
});

describe("native rule saves", () => {
  it("writes expressions and owner rules together in one request", async () => {
    const saved = { ...before, list: { op: "eq", field: "status", value: "open", expression: "status = 'open'" } };
    api.setCollectionRules.mockImplementation(async () => {
      api.getCollectionRules.mockResolvedValue({ collection: "orders", rules: saved });
    });
    const { result } = editor();
    await waitFor(() => expect(result.current.drafts).toBeDefined());
    act(() => result.current.update("list", { mode: "expression", expression: "status = 'open'" }));
    await act(() => result.current.save.mutateAsync());
    expect(api.setCollectionRules).toHaveBeenCalledExactlyOnceWith("orders", { ...before, list: { op: "expression", expression: "status = 'open'" } });
    expect(result.current.guard.dirty).toBe(false);
    expect(result.current.drafts?.update.loaded).toEqual({ op: "owner" });
  });

  it("keeps the edits on screen until the saved rules have been fetched", async () => {
    const saved = { ...before, list: { op: "expression", expression: "status = 'open'" } };
    let finish!: () => void;
    api.setCollectionRules.mockImplementation(async () => {
      api.getCollectionRules.mockReturnValue(new Promise((resolve) => { finish = () => resolve({ collection: "orders", rules: saved }); }));
    });
    const { result } = editor();
    await waitFor(() => expect(result.current.drafts).toBeDefined());
    act(() => result.current.update("list", { mode: "expression", expression: "status = 'open'" }));
    let done!: Promise<unknown>;
    act(() => { done = result.current.save.mutateAsync(); });
    await waitFor(() => expect(api.getCollectionRules).toHaveBeenCalledTimes(2));
    expect(result.current.drafts?.list.mode).toBe("expression");
    await act(async () => { finish(); await done; });
    expect(result.current.drafts?.list.mode).toBe("expression");
    expect(result.current.guard.dirty).toBe(false);
  });

  it("keeps edits available after a failed atomic save", async () => {
    api.setCollectionRules.mockRejectedValue(new Error("network"));
    const { result } = editor();
    await waitFor(() => expect(result.current.drafts).toBeDefined());
    act(() => result.current.update("list", { mode: "expression", expression: "status = 'open'" }));
    await act(() => result.current.save.mutateAsync().catch(() => undefined));
    expect(result.current.guard.dirty).toBe(true);
    expect(result.current.drafts?.list.expression).toBe("status = 'open'");
    expect(result.current.drafts?.update.loaded).toEqual({ op: "owner" });
    expect(api.setCollectionRules).toHaveBeenCalledTimes(1);
  });
  it("previews text IDs without treating them as internal UUIDs", async () => {
    api.listRecords.mockResolvedValue({ items: [{ id: "CaseSensitive" }, { id: "AnotherPublicId" }] });
    api.previewCollectionRule.mockResolvedValue([{ recordId: "CaseSensitive", allowed: true }, { recordId: "AnotherPublicId", allowed: false }]);
    const { result } = editor();
    await waitFor(() => expect(result.current.drafts).toBeDefined());
    await act(() => result.current.preview.mutateAsync("view"));
    expect(api.previewCollectionRule).toHaveBeenCalledExactlyOnceWith("orders", { rule: before.view, action: "view", recordIds: ["CaseSensitive", "AnotherPublicId"] });
  });

});

it("closes public access before loosening rules and stops when closing fails", async () => {
  api.setCollectionUserAccess.mockRejectedValueOnce(new Error("refused"));
  const { result } = editor();
  await waitFor(() => expect(result.current.drafts).toBeDefined());
  act(() => { result.current.setAccess(false); result.current.update("list", { mode: "everyone" }); });
  await act(() => result.current.save.mutateAsync().catch(() => undefined));
  expect(api.setCollectionUserAccess).toHaveBeenCalledWith("orders", false);
  expect(api.setCollectionRules).not.toHaveBeenCalled();
  api.setCollectionUserAccess.mockResolvedValue(undefined);
  api.setCollectionRules.mockResolvedValue(undefined);
  await act(() => result.current.save.mutateAsync());
  expect(api.setCollectionUserAccess.mock.invocationCallOrder[1]).toBeLessThan(api.setCollectionRules.mock.invocationCallOrder[0]);
});

it("does not attach a delayed preview to a newly edited rule", async () => {
  let resolve!: (value: { recordId: string; allowed: boolean }[]) => void;
  api.listRecords.mockResolvedValue({ items: [{ id: "r" }] });
  api.previewCollectionRule.mockReturnValue(new Promise((done) => { resolve = done; }));
  const { result } = editor();
  await waitFor(() => expect(result.current.drafts).toBeDefined());
  act(() => result.current.preview.mutate("view"));
  await waitFor(() => expect(api.previewCollectionRule).toHaveBeenCalled());
  act(() => result.current.update("view", { mode: "admins" }));
  await act(async () => { resolve([{ recordId: "r", allowed: true }]); });
  expect(result.current.results.view).toBeUndefined();
});
