import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as backups from "../api/backups";
import { ApiError } from "../api/http";
import { queryClient as appClient } from "../app/queries";
import * as storage from "../api/storage";
import * as system from "../api/system";
import { setLanguage } from "../i18n";
import { BackupsActions, BackupsView } from "./BackupsView";

const entry: backups.BackupEntry = { id: "20261114-a1", createdAt: "2026-11-14T00:00:00Z", size: 2048, postgresqlMajor: 17 };

function renderAt(search: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <BackupsView /> }], { initialEntries: [`/backups${search}`] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  return router;
}

beforeEach(() => {
  setLanguage("en");
  vi.spyOn(backups, "listBackups").mockResolvedValue([entry]);
  vi.spyOn(backups, "listRunningBackupJobs").mockResolvedValue([]);
  vi.spyOn(storage, "getBackupStorageSettings").mockResolvedValue({ driver: "local" } as storage.BackupStorageSettings);
  vi.spyOn(system, "getSettings").mockRejectedValue(new Error("not needed"));
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("deletes an archive only after its file name is typed", async () => {
  const remove = vi.spyOn(backups, "deleteBackup").mockResolvedValue(undefined);
  renderAt(`?backup=${entry.id}`);
  const pane = within(await screen.findByRole("region", { name: "Backup" }));
  fireEvent.click(await pane.findByRole("button", { name: "Delete" }));
  const dialog = within(screen.getByRole("dialog"));
  const confirm = dialog.getByRole("button", { name: "Delete" });
  expect(confirm).toBeDisabled();
  fireEvent.change(dialog.getByRole("textbox", { name: "Type pgbase-20261114-a1.tar.gz to confirm" }), { target: { value: "pgbase-20261114-a1.tar.gz" } });
  fireEvent.click(confirm);
  await waitFor(() => expect(remove).toHaveBeenCalledWith(entry.id));
});

it("restores only after the file name is typed", async () => {
  const restore = vi.spyOn(backups, "restoreBackup").mockResolvedValue({ id: "job1", operation: "restore", backupId: entry.id, status: "running" } as backups.BackupJob);
  vi.spyOn(backups, "getBackupJob").mockResolvedValue({ id: "job1", operation: "restore", backupId: entry.id, status: "running" } as backups.BackupJob);
  renderAt(`?backup=${entry.id}`);
  fireEvent.click(await screen.findByRole("button", { name: "Restore" }));
  const dialog = within(screen.getByRole("dialog"));
  const confirm = dialog.getByRole("button", { name: "Restore" });
  fireEvent.change(dialog.getByRole("textbox", { name: /to confirm/ }), { target: { value: entry.id } });
  expect(confirm).toBeDisabled();
  fireEvent.change(dialog.getByRole("textbox", { name: /to confirm/ }), { target: { value: "pgbase-20261114-a1.tar.gz" } });
  fireEvent.click(confirm);
  await waitFor(() => expect(restore).toHaveBeenCalledWith(entry.id));
});

it("says so when the linked archive does not exist", async () => {
  renderAt("?backup=missing");
  expect(await screen.findByText("Not found.")).toBeInTheDocument();
});

it("re-reads the archive list after a cancelled upload, since the server may have stored it anyway", async () => {
  const list = vi.mocked(backups.listBackups);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <><BackupsActions /><BackupsView /></> }], { initialEntries: ["/backups"] });
  vi.spyOn(backups, "uploadBackup").mockImplementation((_file, signal) => new Promise((_resolve, reject) => signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")))));
  const { container } = render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  await screen.findByText(entry.id);
  const reads = list.mock.calls.length;
  fireEvent.change(container.querySelector("input[type=file]")!, { target: { files: [new File(["gz"], "late.tar.gz")] } });
  fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(list.mock.calls.length).toBeGreaterThan(reads));
});

it("explains a server without the backup console, with Create and Upload disabled for that reason and no retries", async () => {
  const list = vi.spyOn(backups, "listBackups").mockRejectedValue(new ApiError(404, "not_found", "Not found"));
  // The app's own retry rule, with no delay so a retry would show within the test.
  const client = new QueryClient({ defaultOptions: { queries: { retry: appClient.getDefaultOptions().queries?.retry, retryDelay: 0 } } });
  const router = createMemoryRouter([{ path: "*", element: <><BackupsActions /><BackupsView /></> }], { initialEntries: ["/backups"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  expect(await screen.findByRole("heading", { name: "The backup console is off" })).toBeInTheDocument();
  expect(screen.getByText(/PGBASE_BACKUPS_DIRECTORY/)).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
  for (const control of [screen.getByRole("button", { name: "Create backup" }), screen.getByLabelText("Upload archive")]) {
    expect(control).toBeDisabled();
    expect(control).toHaveAccessibleDescription(/backup directory/);
  }
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(list).toHaveBeenCalledTimes(1);
  expect(backups.listRunningBackupJobs).not.toHaveBeenCalled();
  expect(storage.getBackupStorageSettings).not.toHaveBeenCalled();
});

it("keeps the generic error and its retry for other list failures", async () => {
  vi.spyOn(backups, "listBackups").mockRejectedValue(new ApiError(500, "internal", "boom"));
  renderAt("");
  expect(await screen.findByRole("button", { name: "Retry" })).toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "The backup console is off" })).not.toBeInTheDocument();
});
