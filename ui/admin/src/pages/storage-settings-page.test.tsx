import { StorageMigrationNotFound, StorageMigrationRejected } from "../backend/storage-port";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StorageSettingsPage } from "./storage-settings-page";
import { callsTo } from "../test/backend-script";
import { administrator, scriptedAdmin, sessionWrapper } from "../test/harness";

const local = {
  driver: "local" as const,
  bucket: "",
  region: "",
  endpoint: "",
  accessKey: "",
  usePathStyle: false,
  secretConfigured: false,
  revision: 1,
};
const s3 = {
  driver: "s3" as const,
  bucket: "pgbase-test",
  region: "us-east-1",
  endpoint: "http://127.0.0.1:59000",
  accessKey: "access",
  usePathStyle: true,
  secretConfigured: true,
  revision: 2,
};

describe("file storage settings", () => {
  it("preserves an omitted secret, warns on destination change, and tests only saved settings", async () => {
    const harness = scriptedAdmin();
    vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
    harness.script.storageSettings.push({ value: s3 });
    harness.script.saveStorageSettings.push({
      value: { ...s3, accessKey: "rotated", revision: 3 },
    });
    harness.script.testStorage.push({ value: undefined });
    render(<StorageSettingsPage />, {
      wrapper: sessionWrapper(harness.backend, harness.history),
    });
    await screen.findByDisplayValue("pgbase-test");
    fireEvent.change(screen.getByLabelText("Access key"), {
      target: { value: "rotated" },
    });
    expect(
      screen.getByRole("button", { name: "Test saved S3 connection" }),
    ).toBeDisabled();
    fireEvent.click(
      screen.getByRole("button", { name: "Save storage settings" }),
    );
    await screen.findByText(/saved and active/i);
    const sent = callsTo(harness.script, "saveStorageSettings")[0]?.detail;
    expect(sent).not.toHaveProperty("secretKey");
    expect(sent).toMatchObject({ revision: 2, accessKey: "rotated" });
    fireEvent.click(
      screen.getByRole("button", { name: "Test saved S3 connection" }),
    );
    expect(
      await screen.findByText(/upload, read, and delete succeeded/i),
    ).toBeVisible();
  });
  it("offers migration when a populated destination may need copying", async () => {
    const harness = scriptedAdmin();
    vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
    harness.script.storageSettings.push({ value: local });
    render(<StorageSettingsPage />, {
      wrapper: sessionWrapper(harness.backend, harness.history),
    });
    fireEvent.change(await screen.findByLabelText("Driver"), {
      target: { value: "s3" },
    });
    expect(screen.getByRole("note")).toHaveTextContent(
      "copy and verify existing objects",
    );
  });
});

const copying = { id: "migration-one", status: "copying" as const, copiedObjects: 0, totalObjects: 1 };
const committed = { ...copying, status: "committed" as const, copiedObjects: 1 };
const recoveryKey = () => `${window.location.origin}:pgbase-file-storage-migration:${administrator.id}`;
afterEach(() => sessionStorage.clear());

it("recovers after a transient status error and retries settings refresh after cutover", async () => {
  const harness = scriptedAdmin();
    vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
  sessionStorage.setItem(recoveryKey(), copying.id);
  const get = vi.spyOn(harness.backend.storage, "getFiles").mockResolvedValueOnce(local).mockRejectedValueOnce(new Error("offline")).mockResolvedValue(s3);
  const status = vi.spyOn(harness.backend.storage, "migration").mockRejectedValueOnce(new Error("offline")).mockResolvedValue(committed);
  render(<StorageSettingsPage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  await screen.findByText(/temporarily unavailable/);
  expect(sessionStorage.getItem(recoveryKey())).toBe(copying.id);
  await screen.findByText("Storage migration completed and active.", {}, { timeout: 3000 });
  expect(get).toHaveBeenCalledTimes(3);
  expect(status.mock.calls.length).toBeGreaterThanOrEqual(3);
  expect(screen.getByLabelText("Driver")).toHaveValue("s3");
  expect(screen.getByRole("button", { name: "Test saved S3 connection" })).toBeEnabled();
  expect(sessionStorage.getItem(recoveryKey())).toBeNull();
  fireEvent.change(screen.getByLabelText("Driver"), { target: { value: "local" } });
  expect(screen.getByRole("button", { name: "Preview migration" })).toBeEnabled();
});

it("invalidates a preview when its destination or secret changes and reports start failures", async () => {
  const harness = scriptedAdmin();
    vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
  harness.script.storageSettings.push({ value: local });
  vi.spyOn(harness.backend.storage, "previewMigration").mockResolvedValue({ ...copying, id: "00000000-0000-0000-0000-000000000000", status: "preview" });
  const start = vi.spyOn(harness.backend.storage, "startMigration").mockRejectedValue(new Error("offline"));
  render(<StorageSettingsPage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  fireEvent.change(await screen.findByLabelText("Driver"), { target: { value: "s3" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview migration" }));
  await screen.findByRole("button", { name: "Start migration of 1 objects" });
  fireEvent.change(screen.getByLabelText("Secret key"), { target: { value: "changed-secret" } });
  expect(screen.queryByRole("button", { name: /Start migration/ })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Preview migration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Start migration of 1 objects" }));
  await screen.findByRole("button", { name: "Retry migration start" });
  expect(start.mock.calls[0]?.[0]).toMatchObject({ secretKey: "changed-secret" });
  expect(JSON.stringify(sessionStorage)).not.toContain("changed-secret");
});

it("retains a failed migration for resume or cancellation and reports action failure", async () => {
  const harness = scriptedAdmin();
    vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
  harness.script.storageSettings.push({ value: local });
  sessionStorage.setItem(recoveryKey(), copying.id);
  vi.spyOn(harness.backend.storage, "migration").mockResolvedValue({ ...copying, status: "failed" });
  const resume = vi.spyOn(harness.backend.storage, "resumeMigration").mockRejectedValue(new Error("offline"));
  render(<StorageSettingsPage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  fireEvent.click(await screen.findByRole("button", { name: "Resume migration" }));
  await waitFor(() => expect(resume).toHaveBeenCalledWith(copying.id));
  expect(screen.getByRole("button", { name: "Cancel migration" })).toBeEnabled();
  expect(screen.getByRole("button", { name: "Preview migration" })).toBeDisabled();
  expect(sessionStorage.getItem(recoveryKey())).toBe(copying.id);
});

it("discovers the committed job when the start response is lost", async () => {
  const harness = scriptedAdmin();
  harness.script.storageSettings.push({ value: local }, { value: s3 });
  vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
  vi.spyOn(harness.backend.storage, "migration").mockImplementation(async id => ({ ...committed, id }));
  vi.spyOn(harness.backend.storage, "previewMigration").mockResolvedValue({ ...copying, id: "00000000-0000-0000-0000-000000000000", status: "preview" });
  const start = vi.spyOn(harness.backend.storage, "startMigration").mockRejectedValue(new Error("response lost"));
  render(<StorageSettingsPage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  await waitFor(() => expect(screen.getByLabelText("Driver")).toBeEnabled());
  fireEvent.change(screen.getByLabelText("Driver"), { target: { value: "s3" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview migration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Start migration of 1 objects" }));
  await screen.findByText("Storage migration completed and active.");
  expect(start).toHaveBeenCalledTimes(1);
  expect(screen.getByLabelText("Driver")).toHaveValue("s3");
  expect(sessionStorage.getItem(recoveryKey())).toBeNull();
});

it("retries an uncertain start with the same operation identity", async () => {
  const harness = scriptedAdmin();
  harness.script.storageSettings.push({ value: local }, { value: s3 });
  vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
  vi.spyOn(harness.backend.storage, "previewMigration").mockResolvedValue({ ...copying, id: "00000000-0000-0000-0000-000000000000", status: "preview" });
  const status = vi.spyOn(harness.backend.storage, "migration").mockRejectedValue(new Error("offline"));
  const start = vi.spyOn(harness.backend.storage, "startMigration").mockRejectedValueOnce(new Error("response lost")).mockImplementation(async (_, id) => ({ ...copying, id }));
  render(<StorageSettingsPage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  await waitFor(() => expect(screen.getByLabelText("Driver")).toBeEnabled());
  fireEvent.change(screen.getByLabelText("Driver"), { target: { value: "s3" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview migration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Start migration of 1 objects" }));
  const retry = await screen.findByRole("button", { name: "Retry migration start" });
  expect(screen.getByLabelText("Driver")).toBeDisabled();
  expect(sessionStorage.getItem(recoveryKey())).toBe(start.mock.calls[0]?.[1]);
  status.mockImplementation(async id => ({ ...committed, id }));
  fireEvent.click(retry);
  await screen.findByText("Storage migration completed and active.", {}, { timeout: 2000 });
  expect(start.mock.calls).toHaveLength(2);
  expect(start.mock.calls[1]?.[1]).toBe(start.mock.calls[0]?.[1]);
});

it("allows correcting settings after a definitive rejected start", async () => {
  const harness = scriptedAdmin();
  harness.script.storageSettings.push({ value: local });
  vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
  vi.spyOn(harness.backend.storage, "previewMigration").mockResolvedValue({ ...copying, id: "00000000-0000-0000-0000-000000000000", status: "preview" });
  vi.spyOn(harness.backend.storage, "startMigration").mockRejectedValue(new StorageMigrationRejected("storage_conflict"));
  render(<StorageSettingsPage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  await waitFor(() => expect(screen.getByLabelText("Driver")).toBeEnabled());
  fireEvent.change(screen.getByLabelText("Driver"), { target: { value: "s3" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview migration" }));
  fireEvent.click(await screen.findByRole("button", { name: "Start migration of 1 objects" }));
  await screen.findByText(/Storage migration was rejected/);
  expect(screen.getByLabelText("Driver")).toBeEnabled();
  expect(sessionStorage.getItem(recoveryKey())).toBeNull();
  fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "corrected-bucket" } });
  expect(screen.queryByRole("button", { name: /Start migration of/ })).toBeNull();
});

it("lets a restored database discover its current job when the remembered ID is absent", async () => {
  const harness = scriptedAdmin();
  harness.script.storageSettings.push({ value: local });
  sessionStorage.setItem(recoveryKey(), "job-from-before-restore");
  vi.spyOn(harness.backend.storage, "migration").mockRejectedValue(new StorageMigrationNotFound());
  const latest = vi.spyOn(harness.backend.storage, "latestMigration").mockResolvedValue(null);
  render(<StorageSettingsPage />, { wrapper: sessionWrapper(harness.backend, harness.history) });
  fireEvent.click(await screen.findByRole("button", { name: "Find current migration" }));
  await waitFor(() => expect(screen.getByLabelText("Driver")).toBeEnabled());
  expect(latest).toHaveBeenCalledTimes(1);
  expect(sessionStorage.getItem(recoveryKey())).toBeNull();
});
