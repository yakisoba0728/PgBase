import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { BackupJob, OperationsPort, ScheduledJob } from "../backend/ports";
import { OperationsConsole } from "./operations-console";

const job: ScheduledJob = { id: "job", name: "cleanup", schedule: "@every 5m", enabled: true, registered: true, running: false, nextRunAt: "2026-01-01", attempts: 0, failed: false };
function port(): OperationsPort {
  return { logs: vi.fn().mockResolvedValue({ items: [{ id: 100, at: "now", method: "GET", route: "/records/{id}", status: 200, durationMs: 3 }], nextBefore: 100, oldestId: 1 }), jobs: vi.fn().mockResolvedValue({ workerEnabled: true, items: [job] }), configureJob: vi.fn().mockResolvedValue(undefined), runJob: vi.fn().mockResolvedValue(undefined) };
}
describe("operations console", () => {
  it("does not offer backup writes on a server without managed backups", async () => {
    const backend = port(); backend.backups = vi.fn().mockResolvedValue(null); backend.createBackup = vi.fn();
    render(<OperationsConsole port={backend} />);
    expect(await screen.findByText(/Managed backups are unavailable/)).toBeVisible();
    await waitFor(() => expect(backend.backups).toHaveBeenCalled());
    expect(screen.queryByRole("button", { name: "Create backup" })).toBeNull();
  });
  it("paginates filtered logs and queues existing enabled jobs", async () => {
    const backend = port(); const user = userEvent.setup(); render(<OperationsConsole port={backend} />);
    expect(await screen.findByText("/records/{id}")).toBeVisible();
    await user.selectOptions(screen.getByLabelText("Status class"), "5");
    await waitFor(() => expect(backend.logs).toHaveBeenLastCalledWith(undefined, 5, expect.any(AbortSignal)));
    await user.click(screen.getByRole("button", { name: "Older requests" }));
    await waitFor(() => expect(backend.logs).toHaveBeenLastCalledWith(100, 5, expect.any(AbortSignal)));
    await user.click(screen.getByRole("button", { name: "Queue run" }));
    await waitFor(() => expect(backend.runJob).toHaveBeenCalledWith("job", expect.any(AbortSignal)));
    expect(await screen.findByText("Run queued. Refresh to inspect completion.")).toBeVisible();
  });
  it("does not claim a failed write succeeded and shows fresh job state on retry", async () => {
    const backend = port(); vi.mocked(backend.configureJob).mockRejectedValue(new Error("failed"));
    const user = userEvent.setup(); render(<OperationsConsole port={backend} />);
    const interval = await screen.findByLabelText("Interval for cleanup");
    await user.clear(interval); await user.type(interval, "@every 10m");
    await user.click(screen.getByRole("button", { name: "Save job" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Unable to confirm");
    expect(backend.configureJob).toHaveBeenCalledWith("job", "@every 10m", true, expect.any(AbortSignal));
  });
  it("disables mutation while a lease runs", async () => {
    const backend = port(); vi.mocked(backend.jobs).mockResolvedValue({ workerEnabled: true, items: [{ ...job, running: true }] });
    render(<OperationsConsole port={backend} />);
    expect(await screen.findByRole("button", { name: "Queue run" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save job" })).toBeDisabled();
  });
  it("searches durable logs, shows statistics and supports detail and truncation", async () => {
    const backend = port();
    backend.searchLogs = vi.fn().mockResolvedValue({ items: [{ id: 100, at: "2026-01-01T00:00:00Z", method: "GET", route: "/records/{id}", status: 200, durationMs: 3 }], oldestId: 100 });
    backend.logStatistics = vi.fn().mockResolvedValue({ total: 42, dropped: 2, averageDurationMs: 3.5, statusClasses: { "2xx": 42 } });
    backend.logDetail = vi.fn().mockResolvedValue({ id: 100, at: "2026-01-01T00:00:00Z", method: "GET", route: "/records/{id}", status: 200, durationMs: 3 });
    backend.truncateLogs = vi.fn().mockResolvedValue(undefined);
    backend.configureJobInTimezone = vi.fn().mockResolvedValue(undefined);
    const user = userEvent.setup(); render(<OperationsConsole port={backend} />);
    expect(await screen.findByText(/42 retained requests/)).toBeVisible();
    await user.type(screen.getByLabelText("Method"), "post");
    await waitFor(() => expect(backend.searchLogs).toHaveBeenLastCalledWith(expect.objectContaining({ method: "POST" }), expect.any(AbortSignal)));
    await user.click(screen.getByRole("button", { name: "2026-01-01T00:00:00Z" }));
    expect(await screen.findByText(/Request 100:/)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Truncate request log" }));
    await waitFor(() => expect(backend.truncateLogs).toHaveBeenCalled());
    const timezone = screen.getByLabelText("Timezone for cleanup");
    await user.clear(timezone); await user.type(timezone, "Asia/Seoul");
    await user.click(screen.getByRole("button", { name: "Save job" }));
    await waitFor(() => expect(backend.configureJobInTimezone).toHaveBeenCalledWith("job", "@every 5m", "Asia/Seoul", true, expect.any(AbortSignal)));
  });
  it("creates, uploads, restores and deletes managed backups", async () => {
    const backend=port();const completed={id:"receipt",operation:"create" as const,backupId:"backup-id",status:"completed" as const,phase:"completed",createdAt:"now",updatedAt:"now"};
    backend.backups=vi.fn().mockResolvedValue([{id:"backup-id",createdAt:"2026-01-01",size:1048576,postgresqlMajor:18}]);
    backend.createBackup=vi.fn().mockResolvedValue(completed);backend.uploadBackup=vi.fn().mockResolvedValue({id:"uploaded",createdAt:"now",size:4,postgresqlMajor:18});backend.deleteBackup=vi.fn().mockResolvedValue(undefined);backend.restoreBackup=vi.fn().mockResolvedValue({...completed,operation:"restore"});
    vi.spyOn(window,"confirm").mockReturnValue(true);const user=userEvent.setup();render(<OperationsConsole port={backend}/>);
    expect(await screen.findByText(/PostgreSQL 18/)).toBeVisible();await user.click(screen.getByRole("button",{name:"Create backup"}));expect(await screen.findByText(/create backup backup-id: completed/)).toBeVisible();
    await user.upload(screen.getByLabelText("Upload gzip backup"),new File(["gzip"],"backup.tar.gz",{type:"application/gzip"}));await waitFor(()=>expect(backend.uploadBackup).toHaveBeenCalled());
    await user.click(screen.getByRole("button",{name:"Restore"}));await waitFor(()=>expect(backend.restoreBackup).toHaveBeenCalledWith("backup-id",expect.any(AbortSignal)));
    await user.click(screen.getByRole("button",{name:"Delete"}));await waitFor(()=>expect(backend.deleteBackup).toHaveBeenCalledWith("backup-id",expect.any(AbortSignal)));
  });
  it("ignores an old managed-backup poll after cleanup and refreshes after the current terminal result", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const backend = port();
      const running = { id: "receipt", operation: "restore" as const, backupId: "backup-id", status: "running" as const, phase: "copying", createdAt: "now", updatedAt: "now" };
      backend.backups = vi.fn().mockResolvedValue([{ id: "backup-id", createdAt: "now", size: 1, postgresqlMajor: 18 }]);
      backend.restoreBackup = vi.fn().mockResolvedValue(running);
      let settle!: (value: BackupJob) => void;
      backend.backupJob = vi.fn(() => new Promise<BackupJob>((resolve) => { settle = resolve; }));
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const user = userEvent.setup();
      const old = render(<OperationsConsole port={backend} />);
      await screen.findByRole("button", { name: "Restore" }); await user.click(screen.getByRole("button", { name: "Restore" }));
      await act(async () => {});
      await act(async () => { vi.advanceTimersByTime(1_000); });
      old.unmount(); await act(async () => { settle({ ...running, status: "completed", phase: "completed" }); });
      expect(old.container).toBeEmptyDOMElement();

      backend.backupJob = vi.fn().mockResolvedValue({ ...running, status: "completed", phase: "completed" });
      render(<OperationsConsole port={backend} />);
      await screen.findByRole("button", { name: "Restore" }); await user.click(screen.getByRole("button", { name: "Restore" }));
      const backupReads = vi.mocked(backend.backups!);
      const readsBeforeCompletion = backupReads.mock.calls.length;
      await act(async () => { vi.advanceTimersByTime(1_000); });
      await waitFor(() => expect(backupReads.mock.calls.length).toBeGreaterThan(readsBeforeCompletion));
    } finally { vi.useRealTimers(); }
  });
  it("ignores an old rejected poll after the operations port is replaced", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const running = { id: "receipt", operation: "restore" as const, backupId: "backup-id", status: "running" as const, phase: "copying", createdAt: "now", updatedAt: "now" };
      let rejectOld!: (reason: Error) => void;
      const oldPort = port();
      oldPort.backups = vi.fn().mockResolvedValue([{ id: "backup-id", createdAt: "now", size: 1, postgresqlMajor: 18 }]);
      oldPort.restoreBackup = vi.fn().mockResolvedValue(running);
      oldPort.backupJob = vi.fn(() => new Promise<BackupJob>((_, reject) => { rejectOld = reject; }));
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const user = userEvent.setup();
      const view = render(<OperationsConsole port={oldPort} />);
      await screen.findByRole("button", { name: "Restore" });
      await user.click(screen.getByRole("button", { name: "Restore" }));
      await act(async () => {});
      await act(async () => { vi.advanceTimersByTime(1_000); });
      expect(oldPort.backupJob).toHaveBeenCalledOnce();

      const currentPort = port();
      currentPort.backups = vi.fn().mockResolvedValue([{ id: "backup-id", createdAt: "now", size: 1, postgresqlMajor: 18 }]);
      currentPort.backupJob = vi.fn().mockResolvedValue({ ...running, status: "failed", phase: "failed", error: "current failure" });
      view.rerender(<OperationsConsole port={currentPort} />);
      await act(async () => { rejectOld(new Error("obsolete poll failure")); });
      expect(screen.queryByText(/Backup status is unavailable/)).not.toBeInTheDocument();

      await act(async () => { vi.advanceTimersByTime(1_000); });
      expect(await screen.findByText(/current failure/)).toBeVisible();
      await waitFor(() => expect(currentPort.backups).toHaveBeenCalledTimes(2));
    } finally { vi.useRealTimers(); }
  });
});
