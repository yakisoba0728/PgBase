import { useEffect, useRef, useState, type FormEvent } from "react";
import { sessionFailure } from "../auth/session";
import type { LoginInput } from "../backend/ports";

interface LoginPageProps {
  onLogin(input: LoginInput): Promise<void>;
  sessionError?: string | null;
	onRequestPasswordReset?(email: string): Promise<void>;
	onConfirmPasswordReset?(token: string, newPassword: string): Promise<void>;
}

export function LoginPage({ onLogin, sessionError, onRequestPasswordReset, onConfirmPasswordReset }: LoginPageProps) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
	const [otpCode, setOTPCode] = useState("");
	const [otpRequired, setOTPRequired] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState(false);
	const [recovery, setRecovery] = useState<"none" | "request" | "confirm">("none");
	const [resetToken, setResetToken] = useState("");
	const [recoveryMessage, setRecoveryMessage] = useState("");
  const emailRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const formErrorRef = useRef<HTMLParagraphElement>(null);

  useEffect(() => {
    if (errors.email) emailRef.current?.focus();
    else if (errors.password) passwordRef.current?.focus();
    else if (errors.form) formErrorRef.current?.focus();
  }, [errors]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextErrors: Record<string, string> = {};
    if (!email.trim()) nextErrors.email = "Email is required.";
    if (!password) nextErrors.password = "Password is required.";
    if (Object.keys(nextErrors).length > 0) {
      setErrors(nextErrors);
      return;
    }

    setPending(true);
    setErrors({});
    try {
      await onLogin({ email, password, ...(otpRequired ? { otpCode } : {}) });
    } catch (error) {
		if (sessionFailure(error) === "otp-required") {
			setOTPRequired(true);
			setErrors({ form: "Enter the code from your authenticator app." });
			return;
		}
		setPassword("");
      setErrors({
        form: sessionFailure(error) === "invalid-credentials"
          ? "Email or password is incorrect."
          : "Unable to sign in. Try again.",
      });
    } finally {
      setPending(false);
    }
  }
	async function recover(event: FormEvent<HTMLFormElement>) {
		event.preventDefault(); if (pending) return; setPending(true); setRecoveryMessage("");
		try { if (recovery === "request") { await onRequestPasswordReset?.(email); setRecoveryMessage("If this administrator email exists, a reset link has been sent."); setRecovery("confirm"); } else { await onConfirmPasswordReset?.(resetToken, password); setRecoveryMessage("Password reset. You can now sign in."); setRecovery("none"); setPassword(""); setResetToken(""); } } catch { setRecoveryMessage(recovery === "confirm" ? "The reset link is invalid or expired." : "Unable to request a reset. Try again."); } finally { setPending(false); }
	}

  return <main className="auth-page">
    <section aria-labelledby="login-title" className="auth-card">
      <h1 id="login-title">Sign in to pgBase</h1>
      {recovery === "none" && recoveryMessage && <p role="status">{recoveryMessage}</p>}
      {recovery === "none" ? <form noValidate onSubmit={submit}>
        {sessionError && <p className="form-error" role="alert">{sessionError}</p>}
        {errors.form && <p className="form-error" id="login-form-error" ref={formErrorRef} role="alert" tabIndex={-1}>{errors.form}</p>}
        <label htmlFor="email">Email</label>
        <input aria-describedby={errors.email ? "email-error" : undefined} aria-invalid={errors.email ? true : undefined} id="email" type="email" autoComplete="email" disabled={pending} onChange={(event) => setEmail(event.target.value)} ref={emailRef} value={email} />
        {errors.email && <p className="field-error" id="email-error">{errors.email}</p>}
        <label htmlFor="password">Password</label>
        <input aria-describedby={errors.password ? "password-error" : undefined} aria-invalid={errors.password ? true : undefined} id="password" type="password" autoComplete="current-password" disabled={pending} onChange={(event) => setPassword(event.target.value)} ref={passwordRef} value={password} />
        {errors.password && <p className="field-error" id="password-error">{errors.password}</p>}
			{otpRequired && <><label htmlFor="otp-code">Authenticator code</label><input id="otp-code" inputMode="numeric" autoComplete="one-time-code" maxLength={6} disabled={pending} onChange={(event) => setOTPCode(event.target.value)} value={otpCode} /></>}
        <button disabled={pending} type="submit">{pending ? "Signing in…" : "Sign in"}</button>
		{onRequestPasswordReset && onConfirmPasswordReset && <button type="button" onClick={() => { setRecovery("request"); setRecoveryMessage(""); }}>Forgot administrator password?</button>}
      </form> : <form noValidate onSubmit={recover}><h2>{recovery === "request" ? "Reset administrator password" : "Set a new password"}</h2>{recoveryMessage && <p role="status">{recoveryMessage}</p>}<label>Email<input autoComplete="email" type="email" value={email} onChange={event => setEmail(event.target.value)} required /></label>{recovery === "confirm" && <><label>Reset token<input autoComplete="one-time-code" value={resetToken} onChange={event => setResetToken(event.target.value)} required /></label><label>New password<input autoComplete="new-password" type="password" minLength={12} value={password} onChange={event => setPassword(event.target.value)} required /></label></>}<button disabled={pending} type="submit">{pending ? "Submitting…" : recovery === "request" ? "Send reset link" : "Reset password"}</button><button disabled={pending} type="button" onClick={() => setRecovery("none")}>Back to sign in</button></form>}
    </section>
  </main>;
}
