import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Collection } from "../api/collections";
import * as collections from "../api/collections";
import * as http from "../api/http";
import { ApiError } from "../api/http";
import * as records from "../api/records";
import * as system from "../api/system";
import { setLanguage } from "../i18n";
import * as download from "../app/download";
import { RecordsView } from "./RecordsView";

let client: QueryClient;
beforeEach(() => {
  setLanguage("en");
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} unobserve() {} });
  vi.spyOn(collections, "listCollections").mockResolvedValue({ collections: {} } as collections.CollectionCatalog);
  // Batches off: single deletes, so each id is one deleteRecord call.
  vi.spyOn(system, "getSettings").mockResolvedValue({ batch: { enabled: false, maxRequests: 50 } } as system.RuntimeSettings);
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});
afterEach(() => { cleanup(); client.clear(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function show(at = "/") {
  const collection = { id: "c", name: "posts", type: "base", fields: [] } as unknown as Collection;
  const router = createMemoryRouter([{ path: "*", element: <RecordsView collection={collection} /> }], { initialEntries: [at] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  return router;
}

async function confirmDelete(title?: string) {
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  const dialog = await screen.findByRole("dialog");
  if (title) expect(within(dialog).getByRole("heading").textContent).toBe(title);
  fireEvent.change(within(dialog).getByRole("textbox", { name: "Type posts to confirm" }), { target: { value: "posts" } });
  fireEvent.click(within(dialog).getByRole("button", { name: "Delete" }));
  return dialog;
}

it("deletes selected records only after the collection name is typed", async () => {
  vi.spyOn(records, "listRecords").mockResolvedValue({ items: [{ id: "r1" }, { id: "r2" }] } as records.RecordPage);
  const remove = vi.spyOn(records, "deleteRecord").mockResolvedValue();
  show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  const dialog = await screen.findByRole("dialog");
  const confirm = within(dialog).getByRole("button", { name: "Delete" });
  const phrase = within(dialog).getByRole("textbox", { name: "Type posts to confirm" });
  fireEvent.change(phrase, { target: { value: "delete" } });
  expect(confirm).toBeDisabled();
  fireEvent.change(phrase, { target: { value: "posts" } });
  fireEvent.click(confirm);
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(2));
});

it("reports a partial failure and retries only the failed records", async () => {
  vi.spyOn(records, "listRecords").mockResolvedValue({ items: [{ id: "r1" }, { id: "r2" }] } as records.RecordPage);
  const remove = vi.spyOn(records, "deleteRecord").mockImplementation(async (_collection, id) => {
    if (id === "r2" && remove.mock.calls.length <= 2) throw new ApiError(409, "restricted", "Referenced elsewhere");
  });
  show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  await confirmDelete();
  const task = await screen.findByRole("region", { name: "Deleting records" });
  await within(task).findByText("1 deleted, 1 failed.");
  fireEvent.click(within(task).getByRole("button", { name: "Retry 1 failed" }));
  await waitFor(() => expect(remove).toHaveBeenLastCalledWith("posts", "r2"));
  await waitFor(() => expect(screen.queryByRole("region", { name: "Deleting records" })).toBeNull());
  expect(remove).toHaveBeenCalledTimes(3);
});

it("selects every record matching the filter and deletes them all, not only the loaded rows", async () => {
  const all = ["r1", "r2", "r3", "r4", "r5"].map((id) => ({ id }));
  const list = vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) =>
    options.fields === "id" ? { items: all } : { items: all.slice(0, 2), nextCursor: "next", totalItems: 5 },
  );
  const remove = vi.spyOn(records, "deleteRecord").mockResolvedValue();
  show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  expect(screen.getByText("2 selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Select all 5 matching" }));
  expect(screen.getByText("All 5 matching selected")).toBeTruthy();
  await confirmDelete("Delete all 5 matching records?");
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(5));
  expect(list).toHaveBeenCalledWith("posts", expect.objectContaining({ fields: "id" }));
});

it("drops back to the loaded rows when one is unchecked", async () => {
  vi.spyOn(records, "listRecords").mockResolvedValue({ items: [{ id: "r1" }, { id: "r2" }], nextCursor: "next" } as records.RecordPage);
  show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  fireEvent.click(screen.getByRole("button", { name: "Select all matching" }));
  expect(screen.getByText("All matching records selected")).toBeTruthy();
  fireEvent.click(screen.getByRole("checkbox", { name: "Select r2" }));
  expect(screen.getByText("1 selected")).toBeTruthy();
});

it("shows export progress, and on a failure midway offers the rows fetched so far", async () => {
  const save = vi.spyOn(download, "downloadJson").mockImplementation(() => {});
  // jsdom has no popover API; the menu closes itself after a pick.
  Object.assign(HTMLElement.prototype, { showPopover() {}, hidePopover() {} });
  vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) => {
    if (options.cursor === undefined) return { items: [{ id: "r1" }, { id: "r2" }], nextCursor: "next", totalItems: 4 };
    throw new ApiError(500, "internal", "Server error");
  });
  show();
  await screen.findByRole("checkbox", { name: "Select all loaded records" });
  fireEvent.click(screen.getByRole("button", { name: /Export/ }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "All matching records", hidden: true }));
  const task = await screen.findByRole("region", { name: "Exporting records" });
  await within(task).findByText("Stopped after 2 rows; nothing was saved.");
  fireEvent.click(within(task).getByRole("button", { name: "Save 2 rows" }));
  expect(save).toHaveBeenCalledWith("posts-partial.json", [{ id: "r1" }, { id: "r2" }]);
});

it("deletes through one batch request per chunk when batches are enabled", async () => {
  vi.mocked(system.getSettings).mockResolvedValue({ batch: { enabled: true, maxRequests: 50 } } as system.RuntimeSettings);
  vi.spyOn(records, "listRecords").mockResolvedValue({ items: [{ id: "r1" }, { id: "a/b" }] } as records.RecordPage);
  const single = vi.spyOn(records, "deleteRecord").mockResolvedValue();
  const send = vi.spyOn(http, "request").mockResolvedValue({ results: [] });
  show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  await confirmDelete();
  await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
  expect(send).toHaveBeenCalledWith("POST", "/api/v1/collections/batch", {
    body: { requests: [{ method: "DELETE", url: "/api/v1/collections/posts/records/r1" }, { method: "DELETE", url: "/api/v1/collections/posts/records/a%2Fb" }] },
  });
  expect(single).not.toHaveBeenCalled();
});

it("ends a bulk delete against what is open and selected now, not at the start", async () => {
  vi.spyOn(records, "listRecords").mockResolvedValue({ items: [{ id: "r1" }, { id: "r2" }, { id: "r3" }] } as records.RecordPage);
  vi.spyOn(records, "getRecord").mockReturnValue(new Promise(() => {}));
  let finish: (() => void) | undefined;
  vi.spyOn(records, "deleteRecord").mockImplementation(() => new Promise<void>((resolve) => (finish = resolve)));
  const router = show("/?record=r1");
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select r1" }));
  await confirmDelete();
  await waitFor(() => expect(finish).toBeDefined());
  // Mid-run: another record is opened and another row selected.
  await act(() => router.navigate("/?record=r3"));
  fireEvent.click(screen.getByRole("checkbox", { name: "Select r3" }));
  act(() => finish!());
  await waitFor(() => expect(screen.queryByRole("region", { name: "Deleting records" })).toBeNull());
  expect(router.state.location.search).toBe("?record=r3");
  expect(screen.getByText("1 selected")).toBeTruthy();
});
