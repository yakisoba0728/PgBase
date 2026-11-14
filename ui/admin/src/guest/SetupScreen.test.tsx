import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as session from "../api/session";
import * as system from "../api/system";
import { setLanguage } from "../i18n";
import { SetupScreen } from "./SetupScreen";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("keeps a running restore when switching tabs and back", async () => {
  setLanguage("en");
  const job = { id: "job1", status: "running", phase: "copying" } as Awaited<ReturnType<typeof session.restoreSetupBackup>>;
  vi.spyOn(system, "getSetupStatus").mockResolvedValue({ setupRequired: true });
  const upload = vi.spyOn(session, "uploadSetupBackup").mockResolvedValue({ id: "entry1" } as Awaited<ReturnType<typeof session.uploadSetupBackup>>);
  vi.spyOn(session, "restoreSetupBackup").mockResolvedValue(job);
  vi.spyOn(session, "getSetupBackupJob").mockResolvedValue(job);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/setup"]}><SetupScreen /></MemoryRouter></QueryClientProvider>);

  fireEvent.click(screen.getByRole("tab", { name: "Restore from backup" }));
  const form = screen.getByRole("button", { name: "Upload and restore" }).closest("form")!;
  fireEvent.change(form.querySelector("input[name=setupToken]")!, { target: { value: "token" } });
  fireEvent.change(form.querySelector("input[name=archive]")!, { target: { files: [new File(["x"], "backup.tar.gz")] } });
  fireEvent.submit(form);
  expect(await screen.findByText("Restoring: copying")).toBeInTheDocument();

  fireEvent.click(screen.getByRole("tab", { name: "New" }));
  fireEvent.click(screen.getByRole("tab", { name: "Restore from backup" }));
  expect(screen.getByText("Restoring: copying")).toBeVisible();
  expect(screen.getByRole("button", { name: "Create administrator", hidden: true })).not.toBeVisible();
  expect(upload).toHaveBeenCalledTimes(1);
  cleanup(); client.clear();
});

it("shows upload progress and cancels the upload", async () => {
  setLanguage("en");
  vi.spyOn(system, "getSetupStatus").mockResolvedValue({ setupRequired: true });
  let signal: AbortSignal | undefined;
  vi.spyOn(session, "uploadSetupBackup").mockImplementation((_token, _archive, s, onProgress) => {
    signal = s;
    onProgress?.(0.5);
    return new Promise((_, reject) => s?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError"))));
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter initialEntries={["/setup"]}><SetupScreen /></MemoryRouter></QueryClientProvider>);

  fireEvent.click(screen.getByRole("tab", { name: "Restore from backup" }));
  const form = screen.getByRole("button", { name: "Upload and restore" }).closest("form")!;
  fireEvent.change(form.querySelector("input[name=setupToken]")!, { target: { value: "token" } });
  fireEvent.change(form.querySelector("input[name=archive]")!, { target: { files: [new File(["x"], "backup.tar.gz")] } });
  fireEvent.submit(form);
  const progress = await screen.findByRole("region", { name: "Uploading archive" });
  // jsdom's FormData drops a file set through `files`, so only the measured progress is checked here.
  expect(progress).toHaveTextContent("50%");

  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(signal?.aborted).toBe(true);
  await vi.waitFor(() => expect(screen.queryByRole("region", { name: "Uploading archive" })).toBeNull());
  cleanup(); client.clear();
});
