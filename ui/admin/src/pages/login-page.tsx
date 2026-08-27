import { useState, type FormEvent } from "react";
import { APIClientError } from "../api/client";
import type { LoginInput } from "../api/types";

interface LoginPageProps {
  onLogin(input: LoginInput): Promise<void>;
  sessionError?: string | null;
}

export function LoginPage({ onLogin, sessionError }: LoginPageProps) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState(false);

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
        form: error instanceof APIClientError && error.code === "invalid_credentials"
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
        {errors.form && <p className="form-error" role="alert">{errors.form}</p>}
        <label htmlFor="email">Email</label>
        <input id="email" type="email" autoComplete="email" disabled={pending} onChange={(event) => setEmail(event.target.value)} value={email} />
        {errors.email && <p className="field-error">{errors.email}</p>}
        <label htmlFor="password">Password</label>
        <input id="password" type="password" autoComplete="current-password" disabled={pending} onChange={(event) => setPassword(event.target.value)} value={password} />
        {errors.password && <p className="field-error">{errors.password}</p>}
        <button disabled={pending} type="submit">{pending ? "Signing in…" : "Sign in"}</button>
      </form>
    </section>
  </main>;
}
