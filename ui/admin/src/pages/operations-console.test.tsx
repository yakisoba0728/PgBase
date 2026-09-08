import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { OperationsPort, ScheduledJob } from "../backend/ports";
import { OperationsConsole } from "./operations-console";

const job: ScheduledJob = { id: "job", name: "cleanup", schedule: "@every 5m", enabled: true, registered: true, running: false, nextRunAt: "2026-01-01", attempts: 0, failed: false };
function port(): OperationsPort {
  return { logs: vi.fn().mockResolvedValue({ items: [{ id: 100, at: "now", method: "GET", route: "/records/{id}", status: 200, durationMs: 3 }], nextBefore: 100, oldestId: 1 }), jobs: vi.fn().mockResolvedValue({ workerEnabled: true, items: [job] }), configureJob: vi.fn().mockResolvedValue(undefined), runJob: vi.fn().mockResolvedValue(undefined) };
}
describe("operations console", () => {
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
});
