import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createBrowserRouter, createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as api from "../api/collections";
import { ApiView } from "./ApiView";
import { setLanguage } from "../i18n";
afterEach(() => { cleanup(); window.history.replaceState(null, "", "/"); vi.restoreAllMocks(); });
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

function renderApi(accessEnabled = true, initialEntry = "/") {
  setLanguage("en");
  vi.spyOn(api, "getCollectionRules").mockResolvedValue({ collection: "posts", revision: 1, rules: {} as api.CollectionRules });
  vi.spyOn(api, "getCollectionUserAccess").mockResolvedValue({ collection: "posts", enabled: accessEnabled });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const collection = { name: "posts", type: "base", fields: [] } as unknown as api.Collection;
  window.history.replaceState(null, "", initialEntry);
  const router = createBrowserRouter([{ path: "*", element: <ApiView collection={collection} /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  return { router, client };
}

it("opens the list endpoint by default on desktop and closes without writing a sentinel", async () => {
  const { router, client } = renderApi();
  expect(screen.getByRole("region", { name: "Endpoint" })).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Endpoint" })).toBeNull());
  expect(router.state.location.search).toBe("");
  cleanup(); router.dispose(); client.clear();
});

it("selects nothing by default on small screens", () => {
  vi.stubGlobal("innerWidth", 600);
  const { router, client } = renderApi();
  expect(screen.queryByRole("region", { name: "Endpoint" })).toBeNull();
  vi.unstubAllGlobals();
  cleanup(); router.dispose(); client.clear();
});

it("shows the service URL, permissions and curl independently of user access", async () => {
  const { router, client } = renderApi(false, "/?lang=ts");
  await screen.findByText("User and guest access is off for this collection.");
  fireEvent.click(screen.getByRole("tab", { name: "Service token" }));

  const pane = within(screen.getByRole("region", { name: "Endpoint" }));
  const url = window.location.origin + "/api/v1/service/collections/posts/records";
  expect(router.state.location.search).toContain("as=service");
  expect(screen.queryByText("User and guest access is off for this collection.")).toBeNull();
  expect(pane.getByText(url)).toBeInTheDocument();
  expect(pane.getByText("Required collection permission")).toBeInTheDocument();
  expect(pane.getByText("read")).toBeInTheDocument();
  const sample = pane.getByRole("region", { name: "Code sample" });
  expect(sample.textContent).toContain(`curl --get '${url}'`);
  expect(sample.textContent).toContain('Authorization: Bearer $PGBASE_SERVICE_TOKEN');
  expect(pane.queryByRole("tab", { name: "TypeScript" })).toBeNull();

  fireEvent.click(screen.getByRole("button", { name: /^PATCH\s*\/api\/v1\/service/ }));
  expect(pane.getByText("update")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: /\/api\/v1\/service\/collections\/batch/ }));
  expect(pane.getByText("Per operation and collection (create in this example)")).toBeInTheDocument();
  cleanup(); router.dispose(); client.clear();
});
