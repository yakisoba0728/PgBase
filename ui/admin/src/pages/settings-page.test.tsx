import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { AdminSettings } from "../backend/ports";
import { backendScript } from "../test/backend-script";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";
import { sessionWrapper } from "../test/harness";
import { SettingsPage } from "./settings-page";

const settings: AdminSettings = { batch: { enabled: true, maxRequests: 10, maxBodyBytes: 1000, timeoutMs: 1000 }, logs: { retentionDays: 30, level: "info", includeIP: false, includeAuth: false }, proxy: { trustedProxyCIDRs: [] }, backups: { schedule: "@every 24h", timezone: "UTC", enabled: true, keep: 7 }, application: { theme: "system", accent: "#1d4ed8", branding: "pgBase" }, rateLimits: { admin: { accountMaxFailures: 5, networkMaxFailures: 20, windowSeconds: 900, blockSeconds: 900 }, user: { accountMaxFailures: 5, networkMaxFailures: 20, registrationNetworkMaxFailures: 5, windowSeconds: 900, blockSeconds: 900 } }, revision: 1 };

describe("application settings", () => {
  it("saves log capture controls and backup schedule as one revision", async () => {
    const backend = memoryBackend(backendScript()); const save = vi.fn().mockImplementation(async (value: AdminSettings) => ({ ...value, revision: 2 })); backend.settings = { get: vi.fn().mockResolvedValue(settings), save, reload: vi.fn() };
    const user = userEvent.setup(); render(<SettingsPage />, { wrapper: sessionWrapper(backend, memoryHistory()) });
    await screen.findByDisplayValue("info"); await user.selectOptions(screen.getByLabelText("Level"), "warn"); await user.click(screen.getByLabelText("Include IP addresses")); await user.click(screen.getByLabelText("Include authentication events")); await user.clear(screen.getByLabelText("Schedule")); await user.type(screen.getByLabelText("Schedule"), "@every 6h"); await user.click(screen.getByRole("button", { name: "Save settings" }));
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ revision: 1, logs: expect.objectContaining({ level: "warn", includeIP: true, includeAuth: true }), backups: expect.objectContaining({ schedule: "@every 6h" }) })); expect(await screen.findByText("Settings saved.")).toBeVisible();
  });
});
