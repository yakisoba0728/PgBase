import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import type { Collection, CollectionRules } from "../api/collections";
import { useRulesEditor } from "./useRulesEditor";

const before: CollectionRules = { list: { op: "deny" }, view: { op: "allow" }, create: { op: "deny" }, update: { op: "owner" }, delete: { op: "deny" } };
// After the compat write: list holds the expression, the owner row reads admins only.
const halfway: CollectionRules = { ...before, list: { op: "eq", field: "status", value: "open", pocketbase: "status = 'open'" }, update: { op: "deny" } };
let server = before;
let failReads = 0;
const api = vi.hoisted(() => ({ getCollectionRules: vi.fn(), setCollectionRules: vi.fn(), setPocketBaseRules: vi.fn() }));

vi.mock("../api/collections", () => ({
  getCollectionRules: api.getCollectionRules,
  setCollectionRules: api.setCollectionRules,
  setPocketBaseRules: api.setPocketBaseRules,
  getCollectionUserAccess: async () => ({ enabled: true }),
  setCollectionUserAccess: async () => undefined,
  previewCollectionRule: async () => [],
}));

describe("useRulesEditor split save", () => {
  it("keeps the owner row to restore when the second write fails, even if the refetch fails too", async () => {
    api.getCollectionRules.mockImplementation(async () => {
      if (failReads > 0) {
        failReads--;
        throw new Error("network");
      }
      return { collection: "orders", rules: server };
    });
    api.setPocketBaseRules.mockImplementation(async () => {
      server = halfway;
      failReads = 2; // the read inside the save, then the refetch after it
    });
    api.setCollectionRules.mockImplementation(async (_name: string, rules: CollectionRules) => {
      server = rules;
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useRulesEditor({ name: "orders", type: "base", fields: [] } as unknown as Collection, "/collections/orders"), { wrapper });
    await waitFor(() => expect(result.current.drafts).toBeDefined());
    act(() => result.current.update("list", { mode: "expression", expression: "status = 'open'" }));
    await act(() => result.current.save.mutateAsync().catch(() => undefined));
    await waitFor(() => expect(result.current.partial).toEqual(["update"]));
    expect(api.setCollectionRules).not.toHaveBeenCalled();
    // The edits are saved, but leaving would lose the Retry: the guard still asks.
    expect(result.current.dirty).toBe(false);
    expect(result.current.guard.dirty).toBe(true);
    await act(() => result.current.finish.mutateAsync());
    expect(result.current.guard.dirty).toBe(false);
    expect(server.update).toEqual({ op: "owner" });
    expect(server.list).toMatchObject({ pocketbase: "status = 'open'" });
    expect(result.current.partial).toEqual([]);
  });
});
