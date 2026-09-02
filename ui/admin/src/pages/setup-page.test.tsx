import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { backendScript, callsTo, deferredStep, type BackendScript } from "../test/backend-script";
import { activeSessionExpiresAt, administrator, renderAdmin, scriptedAdmin, type AdminHarness } from "../test/harness";

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
});
