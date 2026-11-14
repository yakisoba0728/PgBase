import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import * as storage from "../api/storage";
import { setLanguage } from "../i18n";
import { MigrationView } from "./MigrationView";
import { migrationQuery } from "./queries";

beforeEach(() => setLanguage("en"));
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

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
