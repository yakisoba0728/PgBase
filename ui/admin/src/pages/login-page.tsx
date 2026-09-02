import { useEffect, useRef, useState, type FormEvent } from "react";
import { sessionFailure } from "../auth/session";
import type { LoginInput } from "../backend/ports";

interface LoginPageProps {
  onLogin(input: LoginInput): Promise<void>;
  sessionError?: string | null;
}

export function LoginPage({ onLogin, sessionError }: LoginPageProps) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState(false);
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
      await onLogin({ email, password });
    } catch (error) {
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

  return <main className="auth-page">
    <section aria-labelledby="login-title" className="auth-card">
      <h1 id="login-title">Sign in to pgBase</h1>
      <form noValidate onSubmit={submit}>
        {sessionError && <p className="form-error" role="alert">{sessionError}</p>}
        {errors.form && <p className="form-error" id="login-form-error" ref={formErrorRef} role="alert" tabIndex={-1}>{errors.form}</p>}
        <label htmlFor="email">Email</label>
        <input aria-describedby={errors.email ? "email-error" : undefined} aria-invalid={errors.email ? true : undefined} id="email" type="email" autoComplete="email" disabled={pending} onChange={(event) => setEmail(event.target.value)} ref={emailRef} value={email} />
        {errors.email && <p className="field-error" id="email-error">{errors.email}</p>}
        <label htmlFor="password">Password</label>
        <input aria-describedby={errors.password ? "password-error" : undefined} aria-invalid={errors.password ? true : undefined} id="password" type="password" autoComplete="current-password" disabled={pending} onChange={(event) => setPassword(event.target.value)} ref={passwordRef} value={password} />
        {errors.password && <p className="field-error" id="password-error">{errors.password}</p>}
        <button disabled={pending} type="submit">{pending ? "Signing in…" : "Sign in"}</button>
      </form>
    </section>
  </main>;
}
