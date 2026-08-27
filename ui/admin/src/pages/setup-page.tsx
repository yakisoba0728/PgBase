import { useState, type FormEvent } from "react";
import { APIClientError } from "../api/client";
import type { SetupInput } from "../api/types";

interface SetupPageProps {
  onSetup(input: SetupInput): Promise<void>;
}

const validationMessage: Record<string, string> = {
  invalid: "Enter a valid value.",
  required: "This field is required.",
};

function serverError(error: unknown, field: string): string | undefined {
  if (!(error instanceof APIClientError)) return undefined;
  const code = error.details[field]?.[0];
  if (field === "email" && code === "invalid") return "Enter a valid email address.";
  return code ? validationMessage[code] ?? "Enter a valid value." : undefined;
}

export function SetupPage({ onSetup }: SetupPageProps) {
  const [setupToken, setSetupToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextErrors: Record<string, string> = {};
    if (!setupToken.trim()) nextErrors.setupToken = "Setup token is required.";
    if (!email.trim()) nextErrors.email = "Email is required.";
    if (!password) nextErrors.password = "Password is required.";
    if (password && password !== confirmation) nextErrors.confirmation = "Passwords do not match.";
    if (Object.keys(nextErrors).length > 0) {
      setErrors(nextErrors);
      return;
    }

    setPending(true);
    setErrors({});
    try {
      await onSetup({ setupToken, email, password });
    } catch (error) {
      setPassword("");
      setConfirmation("");
      setErrors({
        email: serverError(error, "email") ?? "",
        form: error instanceof APIClientError && error.code === "setup_unavailable"
          ? "Administrator setup is no longer available."
          : "Unable to create the administrator. Try again.",
      });
    } finally {
      setPending(false);
    }
  }

  return <main className="auth-page">
    <section aria-labelledby="setup-title" className="auth-card">
      <h1 id="setup-title">Set up pgBase</h1>
      <p>Create the first administrator for this pgBase instance.</p>
      <form noValidate onSubmit={submit}>
        {errors.form && <p className="form-error" role="alert">{errors.form}</p>}
        <label htmlFor="setup-token">Setup token</label>
        <input id="setup-token" autoComplete="off" disabled={pending} onChange={(event) => setSetupToken(event.target.value)} value={setupToken} />
        {errors.setupToken && <p className="field-error">{errors.setupToken}</p>}
        <label htmlFor="email">Email</label>
        <input id="email" type="email" autoComplete="email" disabled={pending} onChange={(event) => setEmail(event.target.value)} value={email} />
        {errors.email && <p className="field-error">{errors.email}</p>}
        <label htmlFor="password">Password</label>
        <input id="password" type="password" autoComplete="new-password" disabled={pending} onChange={(event) => setPassword(event.target.value)} value={password} />
        {errors.password && <p className="field-error">{errors.password}</p>}
        <label htmlFor="confirmation">Confirm password</label>
        <input id="confirmation" type="password" autoComplete="new-password" disabled={pending} onChange={(event) => setConfirmation(event.target.value)} value={confirmation} />
        {errors.confirmation && <p className="field-error">{errors.confirmation}</p>}
        <button disabled={pending} type="submit">{pending ? "Creating administrator…" : "Create administrator"}</button>
      </form>
    </section>
  </main>;
}
