import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as api from "../api/collections";
import * as schema from "../api/schema";
import { useSchemaEditor } from "./useSchemaEditor";
import { drafts } from "./drafts";
import { forgetOperations, operations } from "../app/operations";
import { ApiError } from "../api/http";
afterEach(() => { vi.restoreAllMocks(); drafts.set({}); forgetOperations(); });
function wrapper(client: QueryClient) {
  return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}><MemoryRouter initialEntries={["/new-collection/base"]}>{children}</MemoryRouter></QueryClientProvider>;
}
it("includes custom auth realm fields in the actual preview payload", async () => {
  const preview = vi.spyOn(schema, "previewSchemaImport").mockImplementation(async (document) => ({ document, changes: [], checksum: "c", policyChecksum: "p", baseVersion: 1 }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const scaffolds = { auth: { collection: { fields: [], userAccess: false, rules: null }, authCollection: { fields: [], policy: {}, rules: {}, registrationEnabled: false } } } as unknown as schema.CollectionScaffolds;
  const { result, unmount } = renderHook(() => useSchemaEditor({ type: "auth", catalog: [], scaffolds, basePath: "/new-collection/base" }), { wrapper: wrapper(client) });
  act(() => { result.current.setMeta({ name: "members" }); result.current.addField("text"); });
  act(() => result.current.setPane({ review: true }));
  await waitFor(() => expect(preview).toHaveBeenCalled());
  const document = preview.mock.calls[0][0];
  expect(document.authCollections?.[0].fields?.map((field) => field.name)).toEqual(document.collections[0].fields?.map((field) => field.name));
  expect(document.authCollections?.[0].fields).toHaveLength(1);
  unmount(); client.clear();
});
it("locks draft/pane edits during apply and preserves an uncertain operation identity", async () => {
  const definition = { id: "c", name: "notes", fields: [], indexes: [] } as unknown as api.PlannedCollection;
  vi.spyOn(api, "planCollectionChange").mockResolvedValue({ definition, sql: [], checksum: "c", baseVersion: 1 } as api.CollectionPlan);
  let reject!: (reason: unknown) => void;
  vi.spyOn(api, "applyCollectionChange").mockReturnValue(new Promise((_resolve, fail) => { reject = fail; }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const { result, unmount } = renderHook(() => useSchemaEditor({ type: "base", catalog: [], basePath: "/new-collection/base" }), { wrapper: wrapper(client) });
  act(() => result.current.setMeta({ name: "notes" }));
  act(() => result.current.setPane({ review: true }));
  await waitFor(() => expect(result.current.planQuery.data).toBeDefined());
  act(() => result.current.apply.mutate());
  await waitFor(() => expect(result.current.apply.isPending).toBe(true));
  act(() => { result.current.setMeta({ name: "lost" }); result.current.addField("text"); result.current.discard(); result.current.setPane(null); });
  expect(result.current.draft.name).toBe("notes");
  expect(result.current.draft.fields).toHaveLength(0);
  expect(result.current.reviewing).toBe(true);
  await act(async () => { reject(new ApiError(502, "invalid_response", "lost")); });
  await waitFor(() => expect(result.current.apply.isError).toBe(true));
  expect(operations()[0]?.idempotencyKey).toBeTruthy();
  unmount(); client.clear();
});
