import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Collection, CollectionRules } from "../api/collections";
import { useRulesEditor } from "./useRulesEditor";

const before: CollectionRules = { list: { op: "deny" }, view: { op: "allow" }, create: { op: "deny" }, update: { op: "owner" }, delete: { op: "deny" } };
const api = vi.hoisted(() => ({ getCollectionRules: vi.fn(), setCollectionRules: vi.fn(), previewCollectionRule: vi.fn(), listRecords: vi.fn() }));
vi.mock("../api/collections", () => ({
  getCollectionRules: api.getCollectionRules,
  setCollectionRules: api.setCollectionRules,
  getCollectionUserAccess: async () => ({ enabled: true }),
  setCollectionUserAccess: async () => undefined,
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
