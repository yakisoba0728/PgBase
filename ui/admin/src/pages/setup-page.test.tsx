import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { backendScript, callsTo, deferredStep, type BackendScript } from "../test/backend-script";
import { activeSessionExpiresAt, administrator, renderAdmin, scriptedAdmin, type AdminHarness } from "../test/harness";
import { SetupPage } from "./setup-page";

const password = "correct horse battery staple";

function unsetInstance(): AdminHarness {
  const script = backendScript();
  script.resume.push({ value: { kind: "setup-required" } });
  return scriptedAdmin("/admin/setup", script);
}

async function fillSetup(user: ReturnType<typeof userEvent.setup>, token = "first-run-token", secret = password, confirmation = secret) {
  await user.type(screen.getByLabelText("Setup token"), token);
  await user.type(screen.getByLabelText("Email"), administrator.email);
  await user.type(screen.getByLabelText("Password", { exact: true }), secret);
  await user.type(screen.getByLabelText("Confirm password"), confirmation);
  await user.click(screen.getByRole("button", { name: "Create administrator" }));
}

function attempts(script: BackendScript): number {
  return callsTo(script, "setup").length;
}

describe("administrator setup", () => {
  it("reaches first-run setup and conceals the token until it is revealed", async () => {
    const harness = unsetInstance();
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    expect(harness.history.path()).toBe("/admin/setup");
    const token = screen.getByLabelText("Setup token");
    expect(token).toHaveAttribute("type", "password");

    await user.click(screen.getByRole("button", { name: "Show setup token" }));
    expect(token).toHaveAttribute("type", "text");
    await user.click(screen.getByRole("button", { name: "Hide setup token" }));
    expect(token).toHaveAttribute("type", "password");
  });

  it("validates required values and matching passwords before calling the server", async () => {
    const harness = unsetInstance();
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    await user.click(screen.getByRole("button", { name: "Create administrator" }));
    expect(await screen.findByText("Setup token is required.")).toBeVisible();
    expect(screen.getByText("Email is required.")).toBeVisible();
    expect(screen.getByText("Password is required.")).toBeVisible();
    expect(screen.getByLabelText("Setup token")).toHaveFocus();

    await fillSetup(user, "first-run-token", password, "different password");
    expect(await screen.findByText("Passwords do not match.")).toBeVisible();
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute("aria-describedby", "confirmation-error");
    expect(screen.getByLabelText("Confirm password")).toHaveFocus();
    expect(attempts(harness.script)).toBe(0);
  });

  it("explains the password byte bounds before sending a request the server would reject", async () => {
    const harness = unsetInstance();
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    // 11 bytes: one under the server's minimum, so the screen must catch it.
    await fillSetup(user, "first-run-token", "short-pass");

    expect(await screen.findByText("Password must be 12 to 1024 bytes.")).toBeVisible();
    expect(screen.getByLabelText("Password", { exact: true })).toHaveFocus();
    expect(attempts(harness.script)).toBe(0);
  });

  it("disables the pending form and allowlists the server's own failure message", async () => {
    const harness = unsetInstance();
    const attempt = deferredStep<never>();
    harness.script.setup.push(attempt.step);
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    await fillSetup(user);
    expect(screen.getByRole("button", { name: "Creating administrator…" })).toBeDisabled();

    // Administrator setup errors carry a code and never field details, so the
    // reason alone has to produce the message.
    await act(async () => { attempt.settle({ fault: { kind: "invalid-input" } }); });

    expect(await screen.findByText("Enter a valid email address and a password of 12 to 1024 bytes.")).toBeVisible();
    expect(screen.getByLabelText("Password", { exact: true })).toHaveValue("");
    expect(screen.getByLabelText("Confirm password")).toHaveValue("");
  });

  it.each([
    ["invalid-credentials" as const, "Setup token is invalid."],
    ["setup-unavailable" as const, "Administrator setup is no longer available."],
    ["failed" as const, "Unable to create the administrator. Try again."],
  ])("stays on setup and explains a %s failure safely", async (kind, message) => {
    const harness = unsetInstance();
    harness.script.setup.push({ fault: { kind: kind === "failed" ? "unreachable" : kind } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    await fillSetup(user, "wrong-token");

    expect(await screen.findByText(message)).toBeVisible();
    expect(screen.getByRole("heading", { name: "Set up pgBase" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Sign in to pgBase" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("Password", { exact: true })).toHaveValue("");
    expect(harness.history.path()).toBe("/admin/setup");
  });

  it("moves to the authenticated shell after a successful setup", async () => {
    const harness = unsetInstance();
    harness.script.setup.push({ value: { administrator, expiresAt: activeSessionExpiresAt() } });
    harness.script.catalog.push({ value: { version: 0, collections: [] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    await fillSetup(user);

    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();
    expect(screen.getByText(administrator.email)).toBeVisible();
    expect(callsTo(harness.script, "setup")[0].detail).toMatchObject({ setupToken: "first-run-token", email: administrator.email });
  });

  it("uploads a setup-scoped backup and only starts restore after destructive confirmation", async () => {
		const user = userEvent.setup(); const upload = vi.fn().mockResolvedValue({ id: "initial-backup" }); const restore = vi.fn().mockResolvedValue({ id: "job" }); const status = vi.fn().mockResolvedValue({ id: "job", status: "running" }); vi.spyOn(window, "confirm").mockReturnValue(true);
		render(<SetupPage onRestoreBackup={restore} onSetup={vi.fn()} onSetupBackupJob={status} onUploadBackup={upload} />);
		await user.type(screen.getByLabelText("Setup token"), "first-run-token"); await user.upload(screen.getByLabelText("Gzip backup"), new File(["gzip"], "initial.tar.gz", { type: "application/gzip" })); await user.click(screen.getByRole("button", { name: "Upload and restore backup" }));
		expect(upload).toHaveBeenCalledWith("first-run-token", expect.any(File)); expect(restore).toHaveBeenCalledWith("first-run-token", "initial-backup"); expect(await screen.findByText(/Restore started/)).toBeVisible(); expect(screen.getByRole("button", { name: "Waiting for backup restore…" })).toBeDisabled();
		await new Promise((resolve) => window.setTimeout(resolve, 1_050));
		expect(status).toHaveBeenCalledWith("first-run-token", "job", expect.any(AbortSignal));
	});

  it("does not let an old restore poll update an unmounted setup screen", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let settle!: (job: { id: string; status: string; error?: string }) => void;
      const poll = vi.fn(() => new Promise<{ id: string; status: string; error?: string }>((resolve) => { settle = resolve; }));
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const user = userEvent.setup();
      const view = render(<SetupPage onSetup={vi.fn()} onUploadBackup={vi.fn().mockResolvedValue({ id: "backup" })} onRestoreBackup={vi.fn().mockResolvedValue({ id: "old-job" })} onSetupBackupJob={poll} />);
      await user.type(screen.getByLabelText("Setup token"), "first-run-token");
      await user.upload(screen.getByLabelText("Gzip backup"), new File(["gzip"], "backup.gz"));
      await user.click(screen.getByRole("button", { name: "Upload and restore backup" }));
      await act(async () => {});
      await act(async () => { vi.advanceTimersByTime(1_000); });
      expect(poll).toHaveBeenCalledOnce();
      view.unmount();
      await act(async () => { settle({ id: "old-job", status: "failed", error: "late failure" }); });
      expect(view.container).toBeEmptyDOMElement();
    } finally { vi.useRealTimers(); }
  });

  it("ignores an old poll when the polling port is replaced while restore remains active", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let settleOld!: (job: { id: string; status: string; error?: string }) => void;
      const oldPoll = vi.fn(() => new Promise<{ id: string; status: string; error?: string }>((resolve) => { settleOld = resolve; }));
      const currentPoll = vi.fn().mockResolvedValue({ id: "old-job", status: "completed" });
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const user = userEvent.setup();
      const common = {
        onSetup: vi.fn(),
        onUploadBackup: vi.fn().mockResolvedValue({ id: "backup" }),
        onRestoreBackup: vi.fn().mockResolvedValue({ id: "old-job" }),
      };
      const view = render(<SetupPage {...common} onSetupBackupJob={oldPoll} />);
      await user.type(screen.getByLabelText("Setup token"), "first-run-token");
      await user.upload(screen.getByLabelText("Gzip backup"), new File(["gzip"], "backup.gz"));
      await user.click(screen.getByRole("button", { name: "Upload and restore backup" }));
      await act(async () => {});
      await act(async () => { vi.advanceTimersByTime(1_000); });
      expect(oldPoll).toHaveBeenCalledOnce();

      view.rerender(<SetupPage {...common} onSetupBackupJob={currentPoll} />);
      await act(async () => { settleOld({ id: "old-job", status: "failed", error: "obsolete failure" }); });
      expect(screen.queryByText("obsolete failure")).not.toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Waiting for backup restore…" })).toBeDisabled();

      await act(async () => { vi.advanceTimersByTime(1_000); });
      expect(await screen.findByText(/Restore completed/)).toBeVisible();
      expect(currentPoll).toHaveBeenCalledOnce();
    } finally { vi.useRealTimers(); }
  });

  it("settles the current restore poll on completed and failed outcomes", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const poll = vi.fn().mockResolvedValueOnce({ id: "job", status: "completed" });
      const user = userEvent.setup();
      const first = render(<SetupPage onSetup={vi.fn()} onUploadBackup={vi.fn().mockResolvedValue({ id: "backup" })} onRestoreBackup={vi.fn().mockResolvedValue({ id: "job" })} onSetupBackupJob={poll} />);
      await user.type(screen.getByLabelText("Setup token"), "first-run-token"); await user.upload(screen.getByLabelText("Gzip backup"), new File(["gzip"], "backup.gz")); await user.click(screen.getByRole("button", { name: "Upload and restore backup" })); await act(async () => {});
      await act(async () => { vi.advanceTimersByTime(1_000); });
      expect(screen.getByText(/Restore completed/)).toBeVisible();
      first.unmount();

      const failedPoll = vi.fn().mockResolvedValueOnce({ id: "failed-job", status: "failed", error: "integrity check failed" });
      const secondUser = userEvent.setup();
      render(<SetupPage onSetup={vi.fn()} onUploadBackup={vi.fn().mockResolvedValue({ id: "backup" })} onRestoreBackup={vi.fn().mockResolvedValue({ id: "failed-job" })} onSetupBackupJob={failedPoll} />);
      await secondUser.type(screen.getByLabelText("Setup token"), "first-run-token");
      await secondUser.upload(screen.getByLabelText("Gzip backup"), new File(["gzip"], "backup.gz"));
      await secondUser.click(screen.getByRole("button", { name: "Upload and restore backup" }));
      await act(async () => {});
      await act(async () => { vi.advanceTimersByTime(1_000); });
      expect(screen.getByText("integrity check failed")).toBeVisible();
    } finally { vi.useRealTimers(); }
  });
});
