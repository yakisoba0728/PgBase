import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { BackupStoragePage } from "./backup-storage-page";
import { scriptedAdmin, sessionWrapper } from "../test/harness";

const saved = {
  driver: "s3" as const,
  bucket: "backups",
  prefix: "daily",
  region: "us-east-1",
  endpoint: "http://127.0.0.1:59000",
  accessKey: "access",
  usePathStyle: true,
  retention: 7,
  secretConfigured: true,
  revision: 4,
};

it("saves only dirty settings with the current revision and tests only persisted settings", async () => {
  const harness = scriptedAdmin();
  const get = vi.spyOn(harness.backend.backupStorage, "get").mockResolvedValue(saved);
  const save = vi.spyOn(harness.backend.backupStorage, "save").mockResolvedValue({ ...saved, accessKey: "rotated", revision: 5 });
  const test = vi.spyOn(harness.backend.backupStorage, "test").mockResolvedValue();
  render(<BackupStoragePage />, { wrapper: sessionWrapper(harness.backend, harness.history) });

  await screen.findByDisplayValue("backups");
  expect(screen.getByRole("button", { name: "Save backup storage" })).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Access key"), { target: { value: "rotated" } });
  expect(screen.getByRole("button", { name: "Test backup storage" })).toBeDisabled();
  fireEvent.click(screen.getByRole("button", { name: "Save backup storage" }));

  await screen.findByText("Backup storage settings saved.");
  expect(save.mock.calls[0]?.[0]).toMatchObject({ revision: 4, accessKey: "rotated", retention: 7 });
  expect(save.mock.calls[0]?.[0]).not.toHaveProperty("secretKey");
  expect(screen.getByRole("button", { name: "Save backup storage" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Test backup storage" })).toBeEnabled();

  fireEvent.click(screen.getByRole("button", { name: "Test backup storage" }));
  await screen.findByText(/Saved backup storage upload/);
  expect(test).toHaveBeenCalledTimes(1);
  expect(get).toHaveBeenCalledTimes(1);
});

it("replaces or explicitly clears a secret without ever displaying it", async () => {
  const harness = scriptedAdmin();
  vi.spyOn(harness.backend.backupStorage, "get").mockResolvedValue(saved);
  const save = vi.spyOn(harness.backend.backupStorage, "save")
    .mockResolvedValueOnce({ ...saved, revision: 5 })
    .mockResolvedValueOnce({ ...saved, secretConfigured: false, revision: 6 });
  render(<BackupStoragePage />, { wrapper: sessionWrapper(harness.backend, harness.history) });

  const secret = await screen.findByLabelText("Secret key");
  expect(secret).toHaveAttribute("placeholder", "Configured");
  expect(secret).toHaveValue("");
  fireEvent.change(secret, { target: { value: "replacement" } });
  fireEvent.click(screen.getByRole("button", { name: "Save backup storage" }));
  await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({ secretKey: "replacement", revision: 4 }), expect.any(AbortSignal)));
  await screen.findByText("Backup storage settings saved.");

  fireEvent.click(screen.getByLabelText("Clear saved access and secret keys and use the default credential provider"));
  expect(screen.getByLabelText("Secret key")).toBeDisabled();
  expect(screen.getByLabelText("Access key")).toHaveValue("");
  fireEvent.click(screen.getByRole("button", { name: "Save backup storage" }));
  await waitFor(() => expect(save).toHaveBeenLastCalledWith(expect.objectContaining({ accessKey: "", clearSecret: true, revision: 5 }), expect.any(AbortSignal)));
});

it("rejects invalid retention and disables every action while a save is in flight", async () => {
  const harness = scriptedAdmin();
  vi.spyOn(harness.backend.backupStorage, "get").mockResolvedValue(saved);
  let finishSave: ((value: typeof saved) => void) | undefined;
  const save = vi.spyOn(harness.backend.backupStorage, "save").mockImplementation(() => new Promise((resolve) => { finishSave = resolve; }));
  render(<BackupStoragePage />, { wrapper: sessionWrapper(harness.backend, harness.history) });

  const retention = await screen.findByLabelText("Backups to retain");
  fireEvent.change(retention, { target: { value: "-1" } });
  expect(screen.getByRole("alert")).toHaveTextContent("whole number");
  expect(screen.getByRole("button", { name: "Save backup storage" })).toBeDisabled();
  expect(save).not.toHaveBeenCalled();

  fireEvent.change(retention, { target: { value: "12" } });
  fireEvent.click(screen.getByRole("button", { name: "Save backup storage" }));
  await waitFor(() => expect(save).toHaveBeenCalled());
  expect(screen.getByRole("button", { name: "Save backup storage" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Test backup storage" })).toBeDisabled();
  expect(screen.getByLabelText("Backups to retain")).toBeDisabled();
  finishSave?.({ ...saved, retention: 12, revision: 5 });
  await screen.findByText("Backup storage settings saved.");
});

it("aborts an in-flight request on unmount and ignores its late completion", async () => {
  const harness = scriptedAdmin();
  let signal: AbortSignal | undefined;
  let finish: ((value: typeof saved) => void) | undefined;
  vi.spyOn(harness.backend.backupStorage, "get").mockImplementation((received) => {
    signal = received;
    return new Promise((resolve) => { finish = resolve; });
  });
  const view = render(<BackupStoragePage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  await waitFor(() => expect(signal).toBeDefined());
  view.unmount();
  expect(signal?.aborted).toBe(true);
  finish?.(saved);
});
