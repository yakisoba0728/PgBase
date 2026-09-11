import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { backendScript, callsTo, deferredStep } from "../test/backend-script";
import { activeSessionExpiresAt, administrator, renderAdmin, scriptedAdmin, type AdminHarness } from "../test/harness";
import { LoginPage } from "./login-page";

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

  it("requests an authenticator code after the server requires OTP", async () => {
    const harness = signedOut();
    harness.script.login.push({ fault: { kind: "otp-required" } });
    harness.script.login.push({ value: { administrator, expiresAt: activeSessionExpiresAt() } });
    harness.script.catalog.push({ value: { version: 0, collections: [] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Enter the code from your authenticator app.")).toBeVisible();
    await user.type(screen.getByLabelText("Authenticator code"), "123456");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();
    expect(callsTo(harness.script, "login")[1]?.detail).toMatchObject({ otpCode: "123456" });
  });

	it("runs the enumeration-safe recovery request and token confirmation flow", async () => {
		const user = userEvent.setup(); const request = vi.fn().mockResolvedValue(undefined); const confirm = vi.fn().mockResolvedValue(undefined);
		render(<LoginPage onConfirmPasswordReset={confirm} onLogin={vi.fn()} onRequestPasswordReset={request} />);
		await user.click(screen.getByRole("button", { name: "Forgot administrator password?" }));
		await user.type(screen.getByLabelText("Email"), administrator.email); await user.click(screen.getByRole("button", { name: "Send reset link" }));
		expect(await screen.findByText(/If this administrator email exists/)).toBeVisible(); expect(request).toHaveBeenCalledWith(administrator.email);
		await user.type(screen.getByLabelText("Reset token"), "one-time-token"); await user.type(screen.getByLabelText("New password"), "correct horse battery staple"); await user.click(screen.getByRole("button", { name: "Reset password" }));
		expect(confirm).toHaveBeenCalledWith("one-time-token", "correct horse battery staple"); expect(await screen.findByText("Password reset. You can now sign in.")).toBeVisible();
	});
});
