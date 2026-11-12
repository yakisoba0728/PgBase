import { t, useLocale } from "../i18n";
import { LanguagePicker } from "../i18n/language-picker";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { sessionFailure } from "../auth/session";
import type { LoginInput } from "../backend/ports";
import { Button } from "../components/ui";
import { PasswordInput } from "../components/password-input";

interface LoginPageProps {
  onLogin(input: LoginInput): Promise<void>;
  sessionError?: string | null;
  onRequestPasswordReset?(email: string): Promise<void>;
  onConfirmPasswordReset?(token: string, newPassword: string): Promise<void>;
}

export function LoginPage({ onLogin, sessionError, onRequestPasswordReset, onConfirmPasswordReset }: LoginPageProps) {
  useLocale();
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
  const otpRef = useRef<HTMLInputElement>(null);
  const resetTokenRef = useRef<HTMLInputElement>(null);
  const formErrorRef = useRef<HTMLParagraphElement>(null);

  useEffect(() => {
    if (errors.email) emailRef.current?.focus();
    else if (errors.resetToken) resetTokenRef.current?.focus();
    else if (errors.password) passwordRef.current?.focus();
    else if (errors.otpCode) otpRef.current?.focus();
    else if (errors.form) formErrorRef.current?.focus();
  }, [errors]);

  useEffect(() => { if (otpRequired) otpRef.current?.focus(); }, [otpRequired]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    const nextErrors: Record<string, string> = {};
    if (!email.trim()) nextErrors.email = "Email is required.";
    if (!password) nextErrors.password = "Password is required.";
    if (otpRequired && !/^\d{6}$/.test(otpCode)) nextErrors.otpCode = "Enter the six-digit authenticator code.";
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
        setErrors({});
        return;
      }
      setPassword("");
      setErrors({ form: sessionFailure(error) === "invalid-credentials" ? "Email or password is incorrect." : "Unable to sign in. Try again." });
    } finally {
      setPending(false);
    }
  }

  async function recover(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (pending) return;
    const nextErrors: Record<string, string> = {};
    if (recovery === "request" && !email.trim()) nextErrors.email = "Email is required.";
    if (recovery === "confirm") {
      if (!resetToken.trim()) nextErrors.resetToken = "Reset token is required.";
      const bytes = new TextEncoder().encode(password).length;
      if (bytes < 12 || bytes > 1024) nextErrors.password = "Password must be 12 to 1024 bytes.";
    }
    if (Object.keys(nextErrors).length) { setErrors(nextErrors); return; }
    setErrors({});
    setPending(true);
    setRecoveryMessage("");
    try {
      if (recovery === "request") {
        await onRequestPasswordReset?.(email);
        setRecoveryMessage("If this administrator email exists, a reset link has been sent.");
        setRecovery("confirm");
      } else {
        await onConfirmPasswordReset?.(resetToken, password);
        setRecoveryMessage("Password reset. You can now sign in.");
        setRecovery("none");
        setPassword("");
        setResetToken("");
      }
    } catch {
      setErrors({ form: recovery === "confirm" ? "The reset link is invalid or expired." : "Unable to request a reset. Try again." });
    } finally {
      setPending(false);
    }
  }

  function changeRecovery(next: "none" | "request") {
    setRecovery(next);
    setRecoveryMessage("");
    setErrors({});
    setPassword("");
    setResetToken("");
    setOTPCode("");
    setOTPRequired(false);
  }

  const title = recovery === "none" ? "Sign in to pgBase" : recovery === "request" ? "Reset administrator password" : "Set a new password";
  const description = recovery === "none" ? "Sign in to manage your collections, records and application." : recovery === "request" ? "Enter your administrator email to receive a reset link." : "Use the token from your reset link to set a new password.";

  return <main className="auth-page login-page">
    <div className="login-content">
      <section aria-labelledby="login-title" className="login-panel">
        <LanguagePicker />
        <div className="auth-brand"><span className="auth-brand-mark" aria-hidden="true">pB</span><span>pgBase</span></div>
        <p className="login-context">{t("Administrator access")}</p>
        <h1 id="login-title">{t(title)}</h1>
        <p className="auth-description">{t(description)}</p>
        {recovery === "none" && recoveryMessage && <p className="login-notice" role="status">{t(recoveryMessage)}</p>}
        {recovery === "none" ? <form aria-busy={pending} noValidate onSubmit={submit}>
          {sessionError && <p className="form-error login-feedback" role="alert">{t(sessionError)}</p>}
          {errors.form && <p className="form-error login-feedback" id="login-form-error" ref={formErrorRef} role="alert" tabIndex={-1}>{t(errors.form)}</p>}
          <div className="login-field">
            <label htmlFor="email">{t("Email")}</label>
            <input aria-describedby={errors.email ? "email-error" : undefined} aria-invalid={errors.email ? true : undefined} autoCapitalize="none" autoComplete="email" disabled={pending} id="email" onChange={event => setEmail(event.target.value)} placeholder="name@example.com" ref={emailRef} spellCheck={false} type="email" value={email} />
            {errors.email && <p className="field-error" id="email-error">{t(errors.email)}</p>}
          </div>
          <div className="login-field">
            <div className="login-label-row">
              <label htmlFor="password">{t("Password")}</label>
              {onRequestPasswordReset && onConfirmPasswordReset && <Button aria-label={t("Forgot administrator password?")} className="recovery-button" disabled={pending} onClick={() => changeRecovery("request")} variant="ghost">{t("Forgot password?")}</Button>}
            </div>
            <PasswordInput aria-describedby={errors.password ? "password-error" : undefined} aria-invalid={errors.password ? true : undefined} autoComplete="current-password" disabled={pending} id="password" onChange={event => setPassword(event.target.value)} ref={passwordRef} value={password} />
            {errors.password && <p className="field-error" id="password-error">{t(errors.password)}</p>}
          </div>
          {otpRequired && <div className="login-field"><label htmlFor="otp-code">{t("Authenticator code")}</label><p id="otp-hint">{t("Enter the code from your authenticator app.")}</p><input aria-describedby={errors.otpCode ? "otp-hint otp-error" : "otp-hint"} aria-invalid={errors.otpCode ? true : undefined} ref={otpRef} autoComplete="one-time-code" disabled={pending} id="otp-code" inputMode="numeric" maxLength={6} onChange={event => setOTPCode(event.target.value)} value={otpCode} />{errors.otpCode && <p className="field-error" id="otp-error">{t(errors.otpCode)}</p>}</div>}
          <Button disabled={pending} type="submit">{t(pending ? "Signing in…" : "Sign in")}</Button>
        </form> : <form aria-busy={pending} noValidate onSubmit={recover}>
          {recoveryMessage && <p className="login-notice" role="status">{t(recoveryMessage)}</p>}
          {errors.form && <p className="form-error login-feedback" ref={formErrorRef} role="alert" tabIndex={-1}>{t(errors.form)}</p>}
          <div className="login-field"><label htmlFor="recovery-email">{t("Email")}</label><input aria-describedby={errors.email ? "recovery-email-error" : undefined} aria-invalid={errors.email ? true : undefined} ref={emailRef} autoCapitalize="none" autoComplete="email" disabled={pending} id="recovery-email" onChange={event => setEmail(event.target.value)} required spellCheck={false} type="email" value={email} />{errors.email && <p className="field-error" id="recovery-email-error">{t(errors.email)}</p>}</div>
          {recovery === "confirm" && <>
            <div className="login-field"><label htmlFor="reset-token">{t("Reset token")}</label><input aria-describedby={errors.resetToken ? "reset-token-error" : undefined} aria-invalid={errors.resetToken ? true : undefined} ref={resetTokenRef} autoComplete="off" spellCheck={false} disabled={pending} id="reset-token" onChange={event => setResetToken(event.target.value)} required value={resetToken} />{errors.resetToken && <p className="field-error" id="reset-token-error">{t(errors.resetToken)}</p>}</div>
            <div className="login-field"><label htmlFor="new-password">{t("New password")}</label><PasswordInput aria-describedby={errors.password ? "new-password-error" : "new-password-hint"} aria-invalid={errors.password ? true : undefined} ref={passwordRef} autoComplete="new-password" disabled={pending} id="new-password" minLength={12} onChange={event => setPassword(event.target.value)} required value={password} /><p id="new-password-hint">{t("Use 12 to 1024 bytes for your new password.")}</p>{errors.password && <p className="field-error" id="new-password-error">{t(errors.password)}</p>}</div>
          </>}
          <Button disabled={pending} type="submit">{t(pending ? "Submitting…" : recovery === "request" ? "Send reset link" : "Reset password")}</Button>
          <Button className="login-back" disabled={pending} onClick={() => changeRecovery("none")} variant="secondary">{t("Back to sign in")}</Button>
        </form>}
        <p className="auth-footer">{t("Administrator dashboard \u00b7 PostgreSQL backend")}</p>
      </section>
    </div>
  </main>;
}
