import { screen, waitFor } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { backendScript } from "../test/backend-script";
import { administrator, renderAdmin, scriptedAdmin } from "../test/harness";

describe("session lifecycle", () => {
  it("clears the authenticated shell when the server session expires", async () => {
    const script = backendScript();
    script.resume.push({ value: { kind: "authenticated", session: { administrator, expiresAt: new Date(Date.now() + 250).toISOString() } } });
    script.catalog.push({ value: { version: 0, collections: [] } });
    const harness = scriptedAdmin("/admin/collections", script);
    renderAdmin(harness);

    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();
    expect(await screen.findByRole("heading", { name: "Sign in to pgBase" }, { timeout: 2000 })).toBeVisible();
    expect(harness.history.path()).toBe("/admin/login");
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
