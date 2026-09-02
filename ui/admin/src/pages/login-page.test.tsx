import { act, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { backendScript, callsTo, deferredStep } from "../test/backend-script";
import { activeSessionExpiresAt, administrator, renderAdmin, scriptedAdmin, type AdminHarness } from "../test/harness";

function signedOut(): AdminHarness {
  const script = backendScript();
  script.resume.push({ value: { kind: "anonymous" } });
  return scriptedAdmin("/admin/login", script);
}

describe("administrator login", () => {
  it("shows validation errors and does not submit incomplete credentials", async () => {
    const harness = signedOut();
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Email is required.")).toBeVisible();
    expect(screen.getByText("Password is required.")).toBeVisible();
    expect(screen.getByLabelText("Email")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Email")).toHaveAttribute("aria-describedby", "email-error");
    expect(screen.getByLabelText("Email")).toHaveFocus();
    expect(callsTo(harness.script, "login")).toHaveLength(0);
  });

  it("shows a safe credential error, clears the password and disables the pending form", async () => {
    const harness = signedOut();
    const attempt = deferredStep<never>();
    harness.script.login.push(attempt.step);
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "bad password");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(screen.getByRole("button", { name: "Signing in…" })).toBeDisabled();

    await act(async () => { attempt.settle({ fault: { kind: "invalid-credentials" } }); });

    expect(await screen.findByText("Email or password is incorrect.")).toBeVisible();
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByRole("alert")).toHaveFocus();
  });

  it("moves to the authenticated shell after a successful login", async () => {
    const harness = signedOut();
    harness.script.login.push({ value: { administrator, expiresAt: activeSessionExpiresAt() } });
    harness.script.catalog.push({ value: { version: 0, collections: [] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();
    expect(screen.getByText(administrator.email)).toBeVisible();
    expect(harness.history.path()).toBe("/admin/collections");
  });
});
