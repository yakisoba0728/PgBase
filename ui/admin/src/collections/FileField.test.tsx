import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import type { CollectionField } from "../api/collections";
import * as api from "../api/records";
import { advanceSession, ApiError } from "../api/http";
import { FileField } from "./FileField";
import { createMemoryRouter, RouterProvider } from "react-router";
import type { Collection } from "../api/collections";
import { sessionQuery } from "../app/queries";
import { RecordEditor } from "./RecordEditor";
import { recordFilesQuery, recordQuery } from "./queries";
import { setLanguage } from "../i18n";
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
it("offers atomic replacement for a full required single attachment and lets the server inspect an unknown MIME", async () => {
  setLanguage("en");
  const file = { id: "f", filename: "old.txt", size: 3, contentType: "text/plain" } as api.StoredFile;
  vi.spyOn(api, "listRecordFiles").mockResolvedValue([file]);
  vi.spyOn(api, "getRecord").mockResolvedValue({ record: { id: "r" }, etag: '"latest"' });
  const replace = vi.spyOn(api, "replaceRecordFiles").mockResolvedValue({ record: { id: "r" }, etag: '"next"' });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(recordFilesQuery("notes", "r", "cover").queryKey, [file]);
  const field = { id: "cover", name: "cover", type: "file", required: true, options: { maxSelect: 1, mimeTypes: ["text/plain"] } } as CollectionField;
  render(<QueryClientProvider client={client}><FileField collection="notes" recordId="r" field={field} readOnly={false} staged={[]} onStage={vi.fn()} /></QueryClientProvider>);
  expect(screen.getByText("Replace file")).toBeInTheDocument();
  const picked = new File(["new"], "new.txt", { type: "" });
  fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [picked] } });
  await waitFor(() => expect(replace).toHaveBeenCalledWith("notes", "r", '"latest"', "cover", picked, file.id));
  cleanup(); client.clear();
});

it("refreshes after the server detects a concurrent attachment replacement", async () => {
  setLanguage("en");
  const file = { id: "f", filename: "old.txt", size: 3, contentType: "text/plain" } as api.StoredFile;
  const newer = { ...file, id: "newer", filename: "newer.txt" };
  vi.spyOn(api, "listRecordFiles").mockResolvedValue([newer]);
  vi.spyOn(api, "getRecord").mockResolvedValue({ record: { id: "r" }, etag: '"newer"' });
  const replace = vi.spyOn(api, "replaceRecordFiles").mockRejectedValue(new ApiError(412, "precondition_failed", "Record has changed"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnMount: false } } });
  client.setQueryData(recordFilesQuery("notes", "r", "cover").queryKey, [file]);
  const field = { id: "cover", name: "cover", type: "file", options: { maxSelect: 1 } } as CollectionField;
  render(<QueryClientProvider client={client}><FileField collection="notes" recordId="r" field={field} readOnly={false} staged={[]} onStage={vi.fn()} /></QueryClientProvider>);
  expect(screen.getByRole("button", { name: "Preview old.txt" })).toBeInTheDocument();
  const picked = new File(["new"], "new.txt");
  fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [picked] } });
  await screen.findByRole("button", { name: "Preview newer.txt" });
  expect(replace).toHaveBeenCalledWith("notes", "r", '"newer"', "cover", picked, file.id);
  cleanup(); client.clear();
});

it("keeps other edits and saves with the new ETag after replacing a single file", async () => {
  setLanguage("en");
  const file = { id: "f", filename: "old.txt", size: 3, contentType: "text/plain" } as api.StoredFile;
  vi.spyOn(api, "listRecordFiles").mockResolvedValue([file]);
  let finishReload!: (record: api.RecordWithETag) => void;
  const first = { record: { id: "r", title: "a" }, etag: '"v1"' };
  vi.spyOn(api, "getRecord").mockResolvedValueOnce(first).mockResolvedValueOnce(first)
    .mockReturnValueOnce(new Promise((resolve) => { finishReload = resolve; }));
  vi.spyOn(api, "replaceRecordFiles").mockResolvedValue({ record: { id: "r", title: "a" }, etag: '"v2"' });
  const update = vi.spyOn(api, "updateRecord").mockResolvedValue({ record: { id: "r", title: "b" }, etag: '"v3"' });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(sessionQuery.queryKey, { administrator: { id: "admin", email: "a@example.test", active: true, createdAt: "", updatedAt: "" }, csrfToken: "csrf", expiresAt: "", sqlConsoleEnabled: false });
  client.setQueryData(recordFilesQuery("notes", "r", "cover").queryKey, [file]);
  const collection = { id: "c1", name: "notes", tableName: "notes", createdAt: "", fields: [
    { id: "title", name: "title", type: "text" },
    { id: "cover", name: "cover", type: "file", options: { maxSelect: 1 } },
  ] } as unknown as Collection;
  const router = createMemoryRouter([{ path: "*", element: <RecordEditor collection={collection} recordId="r" onClose={vi.fn()} onCreated={vi.fn()} onCreate={vi.fn()} /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  fireEvent.change(await screen.findByLabelText(/^title/), { target: { value: "b" } });
  fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [new File(["new"], "new.txt", { type: "text/plain" })] } });
  await waitFor(() => expect(screen.getByLabelText(/^title/).closest("form")).toHaveAttribute("inert"));
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText(/^title/), { target: { value: "late" } });
  finishReload({ record: { id: "r", title: "a" }, etag: '"v2"' });
  await waitFor(() => expect(client.getQueryData<api.RecordWithETag>(recordQuery("notes", "r").queryKey)?.etag).toBe('"v2"'));
  await waitFor(() => expect(screen.getByLabelText(/^title/)).toHaveValue("b"));
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(update).toHaveBeenCalledWith("notes", "r", { title: "b" }, { ifMatch: '"v2"' }));
  cleanup(); client.clear();
});

it("does not replace a file when the session changes while fetching its ETag", async () => {
  setLanguage("en");
  const file = { id: "f", filename: "old.txt", size: 3, contentType: "text/plain" } as api.StoredFile;
  vi.spyOn(api, "listRecordFiles").mockResolvedValue([file]);
  let finish!: (value: api.RecordWithETag) => void;
  const read = vi.spyOn(api, "getRecord").mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  const replace = vi.spyOn(api, "replaceRecordFiles").mockResolvedValue({ record: { id: "r" }, etag: '"next"' });
  const settled = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(recordFilesQuery("notes", "r", "cover").queryKey, [file]);
  const field = { id: "cover", name: "cover", type: "file", options: { maxSelect: 1 } } as CollectionField;
  render(<QueryClientProvider client={client}><FileField collection="notes" recordId="r" field={field} readOnly={false} staged={[]} onStage={vi.fn()} onRecordChanged={settled} /></QueryClientProvider>);
  fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [new File(["new"], "new.txt")] } });
  await waitFor(() => expect(read).toHaveBeenCalledTimes(1));
  advanceSession();
  finish({ record: { id: "r" }, etag: '"old-session"' });
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(settled).not.toHaveBeenCalled();
  expect(replace).not.toHaveBeenCalled();
  cleanup(); client.clear();
});

it("does not dispatch the next selected file after the administrator session changes", async () => {
  setLanguage("en");
  vi.spyOn(api, "listRecordFiles").mockResolvedValue([]);
  let finish!: (value: api.StoredFile) => void;
  const upload = vi.spyOn(api, "uploadRecordFile").mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; })).mockResolvedValue({ id: "unexpected" } as api.StoredFile);
  const settled = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(recordFilesQuery("notes", "r", "cover").queryKey, []);
  const field = { id: "cover", name: "cover", type: "file", options: { maxSelect: 2 } } as CollectionField;
  render(<QueryClientProvider client={client}><FileField collection="notes" recordId="r" field={field} readOnly={false} staged={[]} onStage={vi.fn()} onRecordChanged={settled} /></QueryClientProvider>);
  fireEvent.change(document.querySelector('input[type="file"]')!, { target: { files: [new File(["one"], "one.txt"), new File(["two"], "two.txt")] } });
  await waitFor(() => expect(upload).toHaveBeenCalledTimes(1));
  advanceSession();
  finish({ id: "one" } as api.StoredFile);
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(settled).not.toHaveBeenCalled();
  expect(upload).toHaveBeenCalledTimes(1);
  cleanup(); client.clear();
});

it.each(["same", "replacement"])("refreshes a failed removal only in the %s administrator session", async (session) => {
  setLanguage("en");
  const file = { id: "f", filename: "old.txt", size: 3, contentType: "text/plain" } as api.StoredFile;
  vi.spyOn(api, "listRecordFiles").mockResolvedValue([file]);
  let fail!: (error: Error) => void;
  const remove = vi.spyOn(api, "deleteFile").mockImplementation(() => new Promise<void>((_, reject) => { fail = reject; }));
  const settled = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(client, "invalidateQueries");
  client.setQueryData(recordFilesQuery("notes", "r", "cover").queryKey, [file]);
  const field = { id: "cover", name: "cover", type: "file", options: { maxSelect: 1 } } as CollectionField;
  render(<QueryClientProvider client={client}><FileField collection="notes" recordId="r" field={field} readOnly={false} staged={[]} onStage={vi.fn()} onRecordChanged={settled} /></QueryClientProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Delete old.txt" }));
  fireEvent.click(screen.getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(remove).toHaveBeenCalledTimes(1));
  if (session === "replacement") advanceSession();
  fail(new Error("The server did not confirm the removal"));
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(settled).toHaveBeenCalledTimes(session === "same" ? 1 : 0);
  expect(invalidate).toHaveBeenCalledTimes(session === "same" ? 1 : 0);
  cleanup(); client.clear();
});
