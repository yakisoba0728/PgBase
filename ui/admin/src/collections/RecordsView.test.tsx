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

function show(at = "/", collection = { id: "c", name: "posts", type: "base", fields: [] } as unknown as Collection) {
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

it("keeps the original filter on batch deletes, fallbacks and retries", async () => {
  const filter = "id != 'unknown'";
  const rows = [{ id: "r1" }, { id: "r2" }];
  vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) =>
    options.fields === "id" ? { items: rows } : { items: rows.slice(0, 1), nextCursor: "next", totalItems: 2 },
  );
  vi.mocked(system.getSettings).mockResolvedValue({ batch: { enabled: true, maxRequests: 50 } } as system.RuntimeSettings);
  const changed = new ApiError(412, "precondition_failed", "Selection changed");
  const request = vi.spyOn(http, "request").mockRejectedValue(changed);
  const remove = vi.spyOn(records, "deleteRecord").mockImplementation(async (_collection, id) => { if (id === "r2") throw changed; });
  show(`/?filter=${encodeURIComponent(filter)}`);
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  fireEvent.click(screen.getByRole("button", { name: "Select all 2 matching" }));
  await confirmDelete();
  const task = await screen.findByRole("region", { name: "Deleting records" });
  const retry = await within(task).findByRole("button", { name: "Retry 1 failed" });
  expect(request).toHaveBeenCalledWith("POST", "/api/v1/collections/batch", {
    body: { operations: rows.map(({ id }) => ({ operation: "delete", collection: "posts", id, matchFilter: filter })) },
  });
  expect(remove).toHaveBeenCalledWith("posts", "r2", { matchFilter: filter });
  fireEvent.click(retry);
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(3));
  expect(remove).toHaveBeenLastCalledWith("posts", "r2", { matchFilter: filter });
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

it.each([true, false])("follows selected export cursors and labels missing rows as partial (all present: %s)", async (allPresent) => {
  const save = vi.spyOn(download, "downloadBlob").mockImplementation(() => {});
  Object.assign(HTMLElement.prototype, { showPopover() {}, hidePopover() {} });
  const list = vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) => {
    if (!options.filter) return { items: [{ id: "r1" }, { id: "r2" }] };
    if (!options.cursor) return { items: [{ id: "r1" }], nextCursor: "more" };
    return { items: allPresent ? [{ id: "r2" }] : [] };
  });
  const preference = "pgbase.records.columns:paged-export";
  localStorage.setItem(preference, JSON.stringify({ hidden: ["body"] }));
  try {
    show("/", { id: "paged-export", name: "posts", type: "base", fields: [
      { id: "title", name: "title", type: "text" }, { id: "body", name: "body", type: "text" },
    ] } as unknown as Collection);
    fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
    fireEvent.click(screen.getByRole("button", { name: /Export/ }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Selected (2)", hidden: true }));
    await waitFor(() => expect(save).toHaveBeenCalledWith(
      allPresent ? "posts-selected.json" : "posts-selected-partial.json",
      expect.objectContaining({ size: allPresent ? 28 : 15 }),
    ));
    expect(list).toHaveBeenCalledWith("posts", expect.objectContaining({ cursor: "more" }));
  } finally {
    localStorage.removeItem(preference);
  }
});

it("shows export progress, and on a failure midway offers the rows fetched so far", async () => {
  const save = vi.spyOn(download, "downloadBlob").mockImplementation(() => {});
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
  expect(save).toHaveBeenCalledWith("posts-partial.json", expect.objectContaining({ size: 28 }));
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

it("does not start a bulk delete after its administrator changes while settings load", async () => {
  let finish!: (settings: system.RuntimeSettings) => void;
  vi.mocked(system.getSettings).mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  vi.spyOn(records, "listRecords").mockResolvedValue({ items: [{ id: "r1" }, { id: "r2" }] });
  const remove = vi.spyOn(records, "deleteRecord").mockResolvedValue();
  show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  await confirmDelete();
  await waitFor(() => expect(system.getSettings).toHaveBeenCalled());
  http.advanceSession();
  await act(async () => { finish({ batch: { enabled: false, maxRequests: 50 } } as system.RuntimeSettings); });
  expect(remove).not.toHaveBeenCalled();
});

it("does not dispatch remaining bulk deletes after its administrator changes", async () => {
  vi.spyOn(records, "listRecords").mockResolvedValue({ items: Array.from({ length: 8 }, (_, index) => ({ id: `r${index}` })) });
  const finish: (() => void)[] = [];
  const remove = vi.spyOn(records, "deleteRecord").mockImplementation(() => new Promise((resolve) => { finish.push(resolve); }));
  show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select all loaded records" }));
  await confirmDelete();
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(6));
  http.advanceSession();
  remove.mockResolvedValue();
  await act(async () => { finish.forEach((resolve) => resolve()); });
  expect(remove).toHaveBeenCalledTimes(6);
});

it("hides destructive actions while a new filter shows the previous rows", async () => {
  const restore = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "scrollTo");
  Object.defineProperty(HTMLElement.prototype, "scrollTo", { configurable: true, value() {} });
  try {
    vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) =>
      options.filter ? new Promise(() => {}) : { items: [{ id: "old" }] },
    );
    const router = show();
    fireEvent.click(await screen.findByRole("checkbox", { name: "Select old" }));
    expect(screen.getByRole("button", { name: "Delete" })).toBeEnabled();
    await act(() => router.navigate("/?filter=" + encodeURIComponent("id = 'new'")));
    await screen.findByRole("progressbar", { name: "Loading" });
    expect(screen.queryByRole("button", { name: "Delete" })).not.toBeInTheDocument();
  } finally {
    if (restore) Object.defineProperty(HTMLElement.prototype, "scrollTo", restore);
    else Reflect.deleteProperty(HTMLElement.prototype, "scrollTo");
  }
});

it("deletes the records named by an open confirmation even if the filter changes", async () => {
  vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) =>
    ({ items: [{ id: options.filter ? "new" : "old" }] }),
  );
  const remove = vi.spyOn(records, "deleteRecord").mockResolvedValue();
  const router = show();
  fireEvent.click(await screen.findByRole("checkbox", { name: "Select old" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  const dialog = within(await screen.findByRole("dialog"));
  const title = dialog.getByRole("heading").textContent;
  const previousScrollTo = HTMLElement.prototype.scrollTo;
  HTMLElement.prototype.scrollTo = () => {};
  try {
    await act(() => router.navigate("/?filter=" + encodeURIComponent("id = 'new'")));
    await screen.findByRole("checkbox", { name: "Select new" });
  } finally {
    HTMLElement.prototype.scrollTo = previousScrollTo;
  }
  expect(dialog.getByRole("heading").textContent).toBe(title);
  fireEvent.change(dialog.getByRole("textbox", { name: "Type posts to confirm" }), { target: { value: "posts" } });
  fireEvent.click(dialog.getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(remove).toHaveBeenCalledExactlyOnceWith("posts", "old"));
});

it("keeps a ten-page record window and can reset to the first page", async () => {
  const previousScrollTo = HTMLElement.prototype.scrollTo;
  HTMLElement.prototype.scrollTo = () => {};
  try {
    vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) => {
      const page = Number(options.cursor ?? 1);
      return { items: [{ id: `r${page}` }], nextCursor: page <= 11 ? String(page + 1) : undefined, totalItems: page === 1 ? 12 : undefined };
    });
    show();
    await screen.findByRole("checkbox", { name: "Select r1" });
    for (let page = 2; page <= 11; page++) {
      fireEvent.click(screen.getByRole("button", { name: "Load more" }));
      await screen.findByRole("checkbox", { name: `Select r${page}` });
    }
    expect(screen.queryByRole("checkbox", { name: "Select r1" })).not.toBeInTheDocument();
    expect(screen.getByText("10 of 12 loaded")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Back to start" }));
    await screen.findByRole("checkbox", { name: "Select r1" });
    expect(screen.queryByRole("button", { name: "Back to start" })).not.toBeInTheDocument();
  } finally {
    HTMLElement.prototype.scrollTo = previousScrollTo;
  }
});

it("reads complete records when exporting a selection from projected grid results", async () => {
  const collection = { id: "projected", name: "posts", type: "base", fields: [
    { id: "title", name: "title", type: "text" }, { id: "body", name: "body", type: "text" },
  ] } as unknown as Collection;
  const preference = "pgbase.records.columns:projected";
  localStorage.setItem(preference, JSON.stringify({ hidden: ["body"] }));
  try {
    Object.assign(HTMLElement.prototype, { showPopover() {}, hidePopover() {} });
    const save = vi.spyOn(download, "downloadBlob").mockImplementation(() => {});
    const list = vi.spyOn(records, "listRecords").mockImplementation(async (_collection, options = {}) =>
      ({ items: [{ id: "r1", title: "Shown", ...(options.fields ? {} : { body: "Also exported" }) }] }),
    );
    show("/", collection);
    fireEvent.click(await screen.findByRole("checkbox", { name: "Select r1" }));
    fireEvent.click(screen.getByRole("button", { name: "Export" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Selected/, hidden: true }));
    await waitFor(() => expect(save).toHaveBeenCalledOnce());
    expect(list).toHaveBeenLastCalledWith("posts", expect.objectContaining({ filter: "id = 'r1'" }));
    expect(list.mock.lastCall?.[1]?.fields).toBeUndefined();
    const json = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsText(save.mock.calls[0][1]);
    });
    expect(JSON.parse(json)).toEqual([{ id: "r1", title: "Shown", body: "Also exported" }]);
  } finally {
    localStorage.removeItem(preference);
  }
});
