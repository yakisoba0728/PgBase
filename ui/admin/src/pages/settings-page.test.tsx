import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { AdminSettings } from "../backend/ports";
import { backendScript } from "../test/backend-script";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";
import { sessionWrapper } from "../test/harness";
import { SettingsPage } from "./settings-page";

const settings: AdminSettings = { batch: { enabled: true, maxRequests: 10, maxBodyBytes: 1000, timeoutMs: 1000 }, logs: { retentionDays: 30, level: "info", includeIP: false, includeAuth: false }, proxy: { trustedProxyCIDRs: [] }, backups: { schedule: "@every 24h", timezone: "UTC", enabled: true, keep: 7 }, application: { theme: "system", accent: "#1d4ed8", branding: "pgBase" }, rateLimits: { admin: { accountMaxFailures: 5, networkMaxFailures: 20, windowSeconds: 900, blockSeconds: 900 }, user: { accountMaxFailures: 5, networkMaxFailures: 20, registrationNetworkMaxFailures: 5, windowSeconds: 900, blockSeconds: 900 }, enabled: false, rules: [], excludedIPs: [] }, revision: 1 };

describe("application settings", () => {
  it("saves log capture controls and backup schedule as one revision", async () => {
    const backend = memoryBackend(backendScript()); const save = vi.fn().mockImplementation(async (value: AdminSettings) => ({ ...value, revision: 2 })); backend.settings = { get: vi.fn().mockResolvedValue(settings), save, reload: vi.fn() };
    const user = userEvent.setup(); render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) });
    await screen.findByDisplayValue("info"); await user.selectOptions(screen.getByLabelText("Level"), "warn"); await user.click(screen.getByLabelText("Include IP addresses")); await user.click(screen.getByLabelText("Include authentication events")); await user.clear(screen.getByLabelText("Schedule")); await user.type(screen.getByLabelText("Schedule"), "@every 6h"); await user.click(screen.getByRole("button", { name: "Save settings" }));
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ revision: 1, logs: expect.objectContaining({ level: "warn", includeIP: true, includeAuth: true }), backups: expect.objectContaining({ schedule: "@every 6h" }) })); expect(await screen.findByText("Settings saved.")).toBeVisible();
  });

	it("edits the current administrator and canonical IP allowlist through their dedicated endpoints", async () => {
		const backend = memoryBackend(backendScript()); backend.settings = { get: vi.fn().mockResolvedValue(settings), save: vi.fn().mockResolvedValue(settings), reload: vi.fn() }; const update = vi.fn().mockResolvedValue({ id: "admin", email: "new@example.test", active: true, createdAt: "now", updatedAt: "now" }); const setIPs = vi.fn().mockResolvedValue(["10.0.0.0/8"]); backend.profile = { get: vi.fn().mockResolvedValue({ id: "admin", email: "admin@example.test", active: true, createdAt: "now", updatedAt: "now" }), update, ipAllowlist: vi.fn().mockResolvedValue([]), setIPAllowlist: setIPs };
		const user = userEvent.setup(); render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) }); const email = await screen.findByDisplayValue("admin@example.test"); await user.clear(email); await user.type(email, "new@example.test"); await user.click(screen.getByRole("button", { name: "Save administrator email" })); expect(update).toHaveBeenCalledWith({ email: "new@example.test" }); await user.type(screen.getByLabelText("Allowed CIDRs"), "10.0.0.0/8"); await user.click(screen.getByRole("button", { name: "Save IP allowlist" })); expect(setIPs).toHaveBeenCalledWith(["10.0.0.0/8"]);
	});

  it("serializes IP allowlist saves until the active security policy write settles", async () => {
    let settle: (prefixes: string[]) => void = () => {};
    const pending = new Promise<string[]>((resolve) => {
      settle = resolve;
    });
    const backend = memoryBackend(backendScript());
    backend.settings = {
      get: vi.fn().mockResolvedValue(settings),
      save: vi.fn().mockResolvedValue(settings),
      reload: vi.fn(),
    };
    const setIPs = vi.fn().mockReturnValue(pending);
    backend.profile = {
      get: vi.fn().mockResolvedValue({ id: "admin", email: "admin@example.test", active: true, createdAt: "now", updatedAt: "now" }),
      update: vi.fn(),
      ipAllowlist: vi.fn().mockResolvedValue([]),
      setIPAllowlist: setIPs,
    };
    const user = userEvent.setup();
    render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) });
    const input = await screen.findByLabelText("Allowed CIDRs");
    await user.type(input, "10.0.0.0/8");
    await user.click(screen.getByRole("button", { name: "Save IP allowlist" }));

    expect(setIPs).toHaveBeenCalledOnce();
    expect(input).toBeDisabled();
    expect(screen.getByRole("button", { name: "Saving IP allowlist…" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Saving IP allowlist…" }));
    expect(setIPs).toHaveBeenCalledOnce();

    settle(["192.0.2.0/24"]);
    await waitFor(() => expect(input).toHaveValue("192.0.2.0/24"));
  });

  it("keeps profile and allowlist save state independent when one save fails", async () => {
    let rejectProfile: (error: Error) => void = () => {};
    let resolveAllowlist: (prefixes: string[]) => void = () => {};
    const profileSave = new Promise<never>((_, reject) => {
      rejectProfile = reject;
    });
    const allowlistSave = new Promise<string[]>((resolve) => {
      resolveAllowlist = resolve;
    });
    const backend = memoryBackend(backendScript());
    backend.settings = {
      get: vi.fn().mockResolvedValue(settings),
      save: vi.fn().mockResolvedValue(settings),
      reload: vi.fn(),
    };
    backend.profile = {
      get: vi.fn().mockResolvedValue({ id: "admin", email: "admin@example.test", active: true, createdAt: "now", updatedAt: "now" }),
      update: vi.fn().mockReturnValue(profileSave),
      ipAllowlist: vi.fn().mockResolvedValue([]),
      setIPAllowlist: vi.fn().mockReturnValue(allowlistSave),
    };
    const user = userEvent.setup();
    render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) });
    await screen.findByDisplayValue("admin@example.test");
    await user.click(screen.getByRole("button", { name: "Save administrator email" }));
    await user.type(screen.getByLabelText("Allowed CIDRs"), "10.0.0.0/8");
    await user.click(screen.getByRole("button", { name: "Save IP allowlist" }));

    expect(screen.getByRole("button", { name: "Saving administrator email…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Saving IP allowlist…" })).toBeDisabled();
    rejectProfile(new Error("rejected"));
    await screen.findByText("Unable to update administrator email.");
    expect(screen.getByRole("button", { name: "Saving IP allowlist…" })).toBeDisabled();

    resolveAllowlist(["192.0.2.0/24"]);
    await waitFor(() => expect(screen.getByLabelText("Allowed CIDRs")).toHaveValue("192.0.2.0/24"));
  });

  it("adds, validates, removes and persists global API rules without changing authentication limits", async () => {
    const backend = memoryBackend(backendScript()); const save = vi.fn().mockImplementation(async (value: AdminSettings) => value); backend.settings = { get: vi.fn().mockResolvedValue(settings), save, reload: vi.fn() };
    const user = userEvent.setup(); render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) }); await screen.findByText("API request rate limits"); await user.click(screen.getByRole("checkbox", { name: "Enable API request rate limits" })); await user.click(screen.getByRole("button", { name: "Add API rate rule" })); await user.type(screen.getByLabelText("Rule 1 label"), "/health/live"); await user.selectOptions(screen.getByLabelText("Rule 1 audience"), "@guest"); await user.clear(screen.getByLabelText("Rule 1 duration seconds")); await user.type(screen.getByLabelText("Rule 1 duration seconds"), "60"); await user.clear(screen.getByLabelText("Rule 1 maximum requests")); await user.type(screen.getByLabelText("Rule 1 maximum requests"), "1"); await user.type(screen.getByLabelText("Excluded client IPs or CIDRs, one per line"), "192.0.2.0/24"); await user.click(screen.getByRole("button", { name: "Save settings" }));
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ rateLimits: expect.objectContaining({ enabled: true, rules: [{ label: "/health/live", audience: "@guest", duration: 60, maxRequests: 1 }], excludedIPs: ["192.0.2.0/24"], admin: settings.rateLimits.admin, user: settings.rateLimits.user }) })); await user.click(screen.getByRole("button", { name: "Remove rule 1" })); expect(screen.queryByLabelText("Rule 1 label")).toBeNull();
  });

  it("does not submit an invalid global API rule", async () => {
    const backend = memoryBackend(backendScript()); const save = vi.fn(); backend.settings = { get: vi.fn().mockResolvedValue(settings), save, reload: vi.fn() };
    const user = userEvent.setup(); render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) }); await screen.findByText("API request rate limits"); await user.click(screen.getByRole("button", { name: "Add API rate rule" })); await user.click(screen.getByRole("button", { name: "Save settings" })); expect(await screen.findByText(/Each rule label must/)).toBeVisible(); expect(save).not.toHaveBeenCalled();
  });
});
