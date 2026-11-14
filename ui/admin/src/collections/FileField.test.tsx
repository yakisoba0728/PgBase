import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import type { CollectionField } from "../api/collections";
import * as api from "../api/records";
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
  await waitFor(() => expect(replace).toHaveBeenCalledWith("notes", "r", '"latest"', "cover", [picked]));
  cleanup(); client.clear();
});

it("keeps other edits and saves with the new ETag after replacing a single file", async () => {
  setLanguage("en");
  const file = { id: "f", filename: "old.txt", size: 3, contentType: "text/plain" } as api.StoredFile;
  vi.spyOn(api, "listRecordFiles").mockResolvedValue([file]);
  vi.spyOn(api, "getRecord").mockResolvedValueOnce({ record: { id: "r", title: "a" }, etag: '"v1"' }).mockResolvedValueOnce({ record: { id: "r", title: "a" }, etag: '"v1"' }).mockResolvedValue({ record: { id: "r", title: "a" }, etag: '"v2"' });
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
  await waitFor(() => expect(client.getQueryData<api.RecordWithETag>(recordQuery("notes", "r").queryKey)?.etag).toBe('"v2"'));
  await waitFor(() => expect(screen.getByLabelText(/^title/)).toHaveValue("b"));
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(update).toHaveBeenCalledWith("notes", "r", { title: "b" }, { ifMatch: '"v2"' }));
  cleanup(); client.clear();
});
