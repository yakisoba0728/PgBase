import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as api from "../api/collections";
import { ApiView } from "./ApiView";
import { setLanguage } from "../i18n";
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("offers a retry when the collection's rules fail to load", async () => {
  setLanguage("en");
  const rules = { list: { op: "allow" }, view: { op: "allow" }, create: { op: "deny" }, update: { op: "deny" }, delete: { op: "deny" } } as api.CollectionRules;
  const get = vi.spyOn(api, "getCollectionRules").mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ collection: "posts", revision: 1, rules });
  vi.spyOn(api, "getCollectionUserAccess").mockResolvedValue({ collection: "posts", enabled: true });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const collection = { name: "posts", type: "base", fields: [] } as unknown as api.Collection;
  const router = createMemoryRouter([{ path: "*", element: <ApiView collection={collection} /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Retry" })).toBeNull());
  expect(get).toHaveBeenCalledTimes(2);
  cleanup(); client.clear();
});

function renderApi() {
  setLanguage("en");
  vi.spyOn(api, "getCollectionRules").mockResolvedValue({ collection: "posts", revision: 1, rules: {} as api.CollectionRules });
  vi.spyOn(api, "getCollectionUserAccess").mockResolvedValue({ collection: "posts", enabled: true });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const collection = { name: "posts", type: "base", fields: [] } as unknown as api.Collection;
  const router = createMemoryRouter([{ path: "*", element: <ApiView collection={collection} /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  return { router, client };
}

it("opens the list endpoint by default on desktop and closes without writing a sentinel", async () => {
  const { router, client } = renderApi();
  expect(screen.getByRole("region", { name: "Endpoint" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Endpoint" })).toBeNull());
  expect(router.state.location.search).toBe("");
  cleanup(); client.clear();
});

it("selects nothing by default on small screens", () => {
  vi.stubGlobal("innerWidth", 600);
  const { client } = renderApi();
  expect(screen.queryByRole("region", { name: "Endpoint" })).toBeNull();
  vi.unstubAllGlobals();
  cleanup(); client.clear();
});
