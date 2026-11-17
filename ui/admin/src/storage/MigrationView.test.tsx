import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { advanceSession, ApiError } from "../api/http";
import * as notifications from "../components/Toast";
import * as operations from "../app/operations";
import * as storage from "../api/storage";
import { setLanguage } from "../i18n";
import { MigrationView } from "./MigrationView";
import { migrationQuery } from "./queries";

beforeEach(() => setLanguage("en"));
afterEach(() => { cleanup(); operations.forgetOperations(); vi.restoreAllMocks(); });

it.each(["same", "replacement"])("publishes a delayed migration failure only into the %s session", async (session) => {
  vi.spyOn(storage, "getFileStorageSettings").mockResolvedValue({ driver: "local", bucket: "", region: "", endpoint: "", accessKey: "", usePathStyle: false, secretConfigured: false, revision: 1 });
  vi.spyOn(storage, "getLatestStorageMigration").mockResolvedValue({ id: "old-migration", status: "failed", copiedObjects: 1, totalObjects: 2, createdAt: "", updatedAt: "" } as storage.StorageMigrationStatus);
  let fail!: (error: Error) => void;
  const resume = vi.spyOn(storage, "resumeStorageMigration").mockImplementation(() => new Promise((_resolve, reject) => { fail = reject; }));
  const notify = vi.spyOn(notifications, "toast").mockImplementation(() => {});
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <MigrationView /> }], { initialEntries: ["/storage/migration"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Resume" }));
  await waitFor(() => expect(resume).toHaveBeenCalledTimes(1));
  const invalidate = vi.spyOn(client, "invalidateQueries");
  if (session === "replacement") advanceSession();
  fail(new ApiError(403, session === "replacement" ? "session_changed" : "permission_denied", "Previous migration request failed"));
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(notify).toHaveBeenCalledTimes(session === "same" ? 1 : 0);
  expect(invalidate).toHaveBeenCalledTimes(session === "same" ? 1 : 0);
  cleanup(); client.clear();
});

it("does not track an obsolete migration resume receipt in a replacement session", async () => {
  vi.spyOn(storage, "getFileStorageSettings").mockResolvedValue({ driver: "local", bucket: "", region: "", endpoint: "", accessKey: "", usePathStyle: false, secretConfigured: false, revision: 1 });
  const migration = { id: "old-migration", status: "failed", copiedObjects: 1, totalObjects: 2, createdAt: "", updatedAt: "" } as storage.StorageMigrationStatus;
  vi.spyOn(storage, "getLatestStorageMigration").mockResolvedValue(migration);
  let finish!: (value: storage.StorageMigrationStatus) => void;
  const resume = vi.spyOn(storage, "resumeStorageMigration").mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  const notify = vi.spyOn(notifications, "toast").mockImplementation(() => {});
  const track = vi.spyOn(operations, "trackOperation");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <MigrationView /> }], { initialEntries: ["/storage/migration"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("button", { name: "Resume" }));
  await waitFor(() => expect(resume).toHaveBeenCalledTimes(1));
  const invalidate = vi.spyOn(client, "invalidateQueries");
  advanceSession();
  finish({ ...migration, status: "copying" });
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(track).not.toHaveBeenCalled();
  expect(notify).not.toHaveBeenCalled();
  expect(invalidate).not.toHaveBeenCalled();
  cleanup(); client.clear();
});

it("does not enable a migration from a previous session's preview", async () => {
  vi.spyOn(storage, "getFileStorageSettings").mockResolvedValue({ driver: "local", bucket: "", region: "", endpoint: "", accessKey: "", usePathStyle: false, secretConfigured: false, revision: 1 });
  vi.spyOn(storage, "getLatestStorageMigration").mockRejectedValue(new ApiError(404, "not_found", "none"));
  let finish!: (value: storage.StorageMigrationStatus) => void;
  const preview = vi.spyOn(storage, "previewStorageMigration").mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <MigrationView /> }], { initialEntries: ["/storage/migration"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  fireEvent.change(await screen.findByRole("textbox", { name: /Bucket/ }), { target: { value: "archive-bucket" } });
  fireEvent.change(screen.getByRole("textbox", { name: /Region/ }), { target: { value: "us-east-1" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview" }));
  await waitFor(() => expect(preview).toHaveBeenCalledTimes(1));
  advanceSession();
  finish({ totalObjects: 2 } as storage.StorageMigrationStatus);
  await waitFor(() => expect(client.getMutationCache().getAll().at(-1)?.state.status).toBe("error"));
  expect(screen.getByRole("button", { name: "Start migration" })).toBeDisabled();
  cleanup(); client.clear();
});

it("starts a migration only after the destination bucket is typed", async () => {
  vi.spyOn(storage, "getFileStorageSettings").mockResolvedValue({ driver: "local", bucket: "", region: "", endpoint: "", accessKey: "", usePathStyle: false, secretConfigured: false, revision: 1 });
  vi.spyOn(storage, "getLatestStorageMigration").mockRejectedValue(new ApiError(404, "not_found", "none"));
  vi.spyOn(storage, "previewStorageMigration").mockResolvedValue({ totalObjects: 2 } as storage.StorageMigrationStatus);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <MigrationView /> }], { initialEntries: ["/storage/migration"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);

  fireEvent.change(await screen.findByRole("textbox", { name: /Bucket/ }), { target: { value: "archive-bucket" } });
  fireEvent.change(screen.getByRole("textbox", { name: /Region/ }), { target: { value: "us-east-1" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Start migration" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Start migration" }));
  const dialog = within(screen.getByRole("dialog"));
  const confirm = dialog.getByRole("button", { name: "Start migration" });
  expect(confirm).toBeDisabled();
  fireEvent.change(dialog.getByRole("textbox", { name: "Type archive-bucket to confirm" }), { target: { value: "archive-bucket" } });
  expect(confirm).toBeEnabled();
  const start = vi.spyOn(storage, "startStorageMigration").mockReturnValue(new Promise(() => {}));
  fireEvent.keyDown(dialog.getByRole("textbox", { name: "Type archive-bucket to confirm" }), { key: "Enter" });
  fireEvent.submit(dialog.getByRole("textbox", { name: "Type archive-bucket to confirm" }));
  await waitFor(() => expect(start).toHaveBeenCalledTimes(1));
  expect(storage.previewStorageMigration).toHaveBeenCalledTimes(1);
});

it("treats a 404 from the latest migration as none yet, and does not poll it", async () => {
  const latest = vi.spyOn(storage, "getLatestStorageMigration").mockRejectedValue(new ApiError(404, "not_found", "none"));
  const client = new QueryClient();
  const options = migrationQuery(null);
  await expect(client.fetchQuery(options)).resolves.toBeNull();
  expect(latest).toHaveBeenCalledTimes(1);
  const query = client.getQueryCache().find({ queryKey: options.queryKey })!;
  expect(typeof options.refetchInterval === "function" && options.refetchInterval(query as never)).toBe(false);
});

it("explains a server without file storage on the migration screen", async () => {
  vi.spyOn(storage, "getFileStorageSettings").mockRejectedValue(new ApiError(404, "not_found", "Not found"));
  vi.spyOn(storage, "getLatestStorageMigration").mockRejectedValue(new ApiError(404, "not_found", "none"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <MigrationView /> }], { initialEntries: ["/storage/migration"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  expect(await screen.findByRole("heading", { name: "File storage is off" })).toBeInTheDocument();
});
