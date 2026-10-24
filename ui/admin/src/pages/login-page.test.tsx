import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { backendScript, callsTo, deferredStep } from "../test/backend-script";
import { activeSessionExpiresAt, administrator, renderAdmin, scriptedAdmin, type AdminHarness } from "../test/harness";
import { LoginPage } from "./login-page";
import { applyWorkspaceTheme } from "../components/workspace-theme";

function signedOut(): AdminHarness {
  const script = backendScript();
  script.resume.push({ value: { kind: "anonymous" } });
  return scriptedAdmin("/admin/login", script);
}

describe("administrator login", () => {
  it("uses system appearance for an anonymous session after a previous saved theme", async () => {
    applyWorkspaceTheme({ theme: "dark", accent: "#7460a2", branding: "Previous workspace" });
    renderAdmin(signedOut());
    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    expect(document.documentElement.dataset.theme).toBe("system");
    expect(document.documentElement.dataset.accent).toBe("theme");
    expect(document.title).toBe("pgBase administration");
  });

  it("reveals the password without submitting and hides it after credentials are cleared", async () => {
    const harness = signedOut();
    harness.script.login.push({ fault: { kind: "invalid-credentials" } });
    const user = userEvent.setup();
    renderAdmin(harness);
    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "bad password");
    await user.click(screen.getByRole("button", { name: "Show password" }));
    expect(screen.getByLabelText("Password")).toHaveAttribute("type", "text");
    expect(screen.getByLabelText("Password")).toHaveValue("bad password");
    expect(screen.getByRole("button", { name: "Hide password" })).toHaveAttribute("aria-pressed", "true");
    expect(callsTo(harness.script, "login")).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await screen.findByText("Email or password is incorrect.");
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByLabelText("Password")).toHaveAttribute("type", "password");
  });

  it("clears the sign-in password when entering and leaving recovery", async () => {
    const user = userEvent.setup();
    render(<LoginPage onLogin={vi.fn()} onRequestPasswordReset={vi.fn()} onConfirmPasswordReset={vi.fn()} />);
    await user.type(screen.getByLabelText("Password"), "private sign-in password");
    await user.click(screen.getByRole("button", { name: "Forgot administrator password?" }));
    expect(screen.getByRole("heading", { name: "Reset administrator password" })).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Back to sign in" }));
    expect(screen.getByLabelText("Password")).toHaveValue("");
  });

  it("blocks recovery and password toggling while sign-in is pending", async () => {
    let finish!: () => void;
    const login = new Promise<void>(resolve => { finish = resolve; });
    const request = vi.fn();
    const user = userEvent.setup();
    render(<LoginPage onLogin={() => login} onRequestPasswordReset={request} onConfirmPasswordReset={vi.fn()} />);
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "pending password");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(screen.getByRole("button", { name: "Forgot administrator password?" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Show password" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Forgot administrator password?" }));
    expect(request).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    await act(async () => { finish(); });
  });

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
    expect(screen.getByRole("button", { name: "Show password" })).toBeDisabled();

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
