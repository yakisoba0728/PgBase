import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as api from "../api/collections";
import { RulesView } from "./RulesView";
import { setLanguage } from "../i18n";
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("shows a declarative view's source policy read-only and links to its editable source", async () => {
  setLanguage("en");
  const rules = { list: { op: "allow" }, view: { op: "allow" }, create: { op: "deny" }, update: { op: "deny" }, delete: { op: "deny" } } as api.CollectionRules;
  const get = vi.spyOn(api, "getCollectionRules").mockResolvedValue({ collection: "source", revision: 1, rules });
  vi.spyOn(api, "getCollectionUserAccess").mockResolvedValue({ collection: "source", enabled: true });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const collection = { name: "view", type: "view", fields: [], view: { source: "source" } } as unknown as api.Collection;
  const router = createMemoryRouter([{ path: "*", element: <RulesView collection={collection} basePath="/collections/view" /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  await waitFor(() => expect(screen.getByRole("link", { name: "source" })).toHaveAttribute("href", "/collections/source/rules"));
  expect(get).toHaveBeenCalledWith("source", expect.any(AbortSignal));
  expect(screen.getByRole("checkbox")).toBeDisabled();
  expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  cleanup(); client.clear();
});

function renderRules() {
  setLanguage("en");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const collection = { name: "posts", type: "base", fields: [] } as unknown as api.Collection;
  const router = createMemoryRouter([{ path: "*", element: <RulesView collection={collection} basePath="/collections/posts" /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  return client;
}

it("offers a retry when the rules fail to load", async () => {
  const rules = { list: { op: "allow" }, view: { op: "allow" }, create: { op: "deny" }, update: { op: "deny" }, delete: { op: "deny" } } as api.CollectionRules;
  const get = vi.spyOn(api, "getCollectionRules").mockRejectedValueOnce(new Error("offline")).mockResolvedValue({ collection: "posts", revision: 1, rules });
  vi.spyOn(api, "getCollectionUserAccess").mockResolvedValue({ collection: "posts", enabled: true });
  const client = renderRules();
  fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
  await screen.findAllByRole("radiogroup");
  expect(get).toHaveBeenCalledTimes(2);
  cleanup(); client.clear();
});

it("makes each rule's modes one tab stop that arrow keys move through", async () => {
  const rules = { list: { op: "allow" }, view: { op: "allow" }, create: { op: "deny" }, update: { op: "deny" }, delete: { op: "deny" } } as api.CollectionRules;
  vi.spyOn(api, "getCollectionRules").mockResolvedValue({ collection: "posts", revision: 1, rules });
  vi.spyOn(api, "getCollectionUserAccess").mockResolvedValue({ collection: "posts", enabled: true });
  const client = renderRules();
  const group = (await screen.findAllByRole("radiogroup"))[0];
  const radios = within(group).getAllByRole("radio");
  const checked = radios.find((radio) => radio.getAttribute("aria-checked") === "true")!;
  expect(radios.filter((radio) => radio.tabIndex === 0)).toEqual([checked]);
  checked.focus();
  fireEvent.keyDown(checked, { key: "ArrowRight" });
  const next = radios[radios.indexOf(checked) + 1];
  expect(next).toHaveFocus();
  expect(next).toHaveAttribute("aria-checked", "true");
  expect(next.tabIndex).toBe(0);
  cleanup(); client.clear();
});
