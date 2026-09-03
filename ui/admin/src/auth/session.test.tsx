import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { backendScript, deferredStep } from "../test/backend-script";
import { activeSessionExpiresAt, administrator, renderAdmin, scriptedAdmin } from "../test/harness";

afterEach(() => vi.useRealTimers());

describe("session lifecycle", () => {
  it("clears the authenticated shell when the server session expires", async () => {
    // The expiry timer is the behaviour under test, so it runs on a clock the
    // test owns rather than on a real deadline the machine could miss.
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const script = backendScript();
    script.resume.push({ value: { kind: "authenticated", session: { administrator, expiresAt: new Date(Date.now() + 60_000).toISOString() } } });
    script.catalog.push({ value: { version: 0, collections: [] } });
    const harness = scriptedAdmin("/admin/collections", script);
    renderAdmin(harness);

    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();

    await act(async () => { await vi.advanceTimersByTimeAsync(60_001); });

    expect(screen.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    expect(harness.history.path()).toBe("/admin/login");
  });

  it("keeps a fresh login when an old logout finishes later", async () => {
    const script = backendScript();
    script.resume.push({ value: { kind: "authenticated", session: { administrator, expiresAt: activeSessionExpiresAt() } } });
    script.catalog.push({ value: { version: 0, collections: [] } }, { value: { version: 0, collections: [] } });
    const oldLogout = deferredStep<void>();
    script.logout.push(oldLogout.step);
    script.status.push({ fault: { kind: "unauthorized" } });
    script.login.push({ value: { administrator, expiresAt: activeSessionExpiresAt() } });
    const harness = scriptedAdmin("/admin/collections", script);
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "pgBase administration" });
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    await expect(harness.backend.system.status()).rejects.toThrow();
    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "fresh password");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByRole("heading", { name: "pgBase administration" });

    await act(async () => { oldLogout.settle({ value: undefined }); });

    expect(screen.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
  });

  it.each([
    ["setup-unknown" as const, "Unable to check administrator setup status. Please retry."],
    ["unreachable" as const, "Unable to check the current session. Please sign in."],
  ])("explains a %s resume before asking for credentials", async (kind, message) => {
    const script = backendScript();
    script.resume.push({ value: { kind } });
    const harness = scriptedAdmin("/admin/collections", script);
    renderAdmin(harness);

    expect(await screen.findByText(message)).toBeVisible();
    await waitFor(() => expect(harness.history.path()).toBe("/admin/login"));
  });

  it("sends an unset instance to first-run setup", async () => {
    const script = backendScript();
    script.resume.push({ value: { kind: "setup-required" } });
    const harness = scriptedAdmin("/admin/collections", script);
    renderAdmin(harness);

    expect(await screen.findByRole("heading", { name: "Set up pgBase" })).toBeVisible();
    expect(harness.history.path()).toBe("/admin/setup");
  });
});
