import { useEffect, useRef, useState, type FormEvent } from "react";
import { APIClientError } from "../api/client";
import type { SetupInput } from "../api/types";

interface SetupPageProps {
  onSetup(input: SetupInput): Promise<void>;
}

type SetupField = "setupToken" | "email" | "password" | "confirmation";
type SetupErrors = Partial<Record<SetupField | "form", string>>;

const serverMessages: Record<Exclude<SetupField, "confirmation">, Record<string, string>> = {
  setupToken: { invalid: "Setup token is invalid.", required: "Setup token is required." },
  email: { invalid: "Enter a valid email address.", required: "Email is required." },
  password: {
    invalid: "Password does not meet requirements.",
    required: "Password is required.",
    too_short: "Password does not meet requirements.",
    weak: "Password does not meet requirements.",
  },
};

function safeServerErrors(error: unknown): SetupErrors {
  if (!(error instanceof APIClientError)) return {};
  return Object.fromEntries(
    (Object.keys(serverMessages) as Array<Exclude<SetupField, "confirmation">>).flatMap((field) => {
      const message = serverMessages[field][error.details[field]?.[0] ?? ""];
      return message ? [[field, message]] : [];
    }),
  ) as SetupErrors;
}

export function SetupPage({ onSetup }: SetupPageProps) {
  const [setupToken, setSetupToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [showSetupToken, setShowSetupToken] = useState(false);
  const [errors, setErrors] = useState<SetupErrors>({});
  const [pending, setPending] = useState(false);
  const setupTokenRef = useRef<HTMLInputElement>(null);
  const emailRef = useRef<HTMLInputElement>(null);
  const passwordRef = useRef<HTMLInputElement>(null);
  const confirmationRef = useRef<HTMLInputElement>(null);
  const formErrorRef = useRef<HTMLParagraphElement>(null);

  useEffect(() => {
    if (errors.setupToken) setupTokenRef.current?.focus();
    else if (errors.email) emailRef.current?.focus();
    else if (errors.password) passwordRef.current?.focus();
    else if (errors.confirmation) confirmationRef.current?.focus();
    else if (errors.form) formErrorRef.current?.focus();
  }, [errors]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextErrors: SetupErrors = {};
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
        ...safeServerErrors(error),
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
        {errors.form && <p className="form-error" id="setup-form-error" ref={formErrorRef} role="alert" tabIndex={-1}>{errors.form}</p>}
        <label htmlFor="setup-token">Setup token</label>
        <input aria-describedby={errors.setupToken ? "setup-token-error" : undefined} aria-invalid={errors.setupToken ? true : undefined} id="setup-token" type={showSetupToken ? "text" : "password"} autoComplete="off" disabled={pending} onChange={(event) => setSetupToken(event.target.value)} ref={setupTokenRef} value={setupToken} />
        <button aria-pressed={showSetupToken} disabled={pending} onClick={() => setShowSetupToken((visible) => !visible)} type="button">{showSetupToken ? "Hide setup token" : "Show setup token"}</button>
        {errors.setupToken && <p className="field-error" id="setup-token-error">{errors.setupToken}</p>}
        <label htmlFor="email">Email</label>
        <input aria-describedby={errors.email ? "email-error" : undefined} aria-invalid={errors.email ? true : undefined} id="email" type="email" autoComplete="email" disabled={pending} onChange={(event) => setEmail(event.target.value)} ref={emailRef} value={email} />
        {errors.email && <p className="field-error" id="email-error">{errors.email}</p>}
        <label htmlFor="password">Password</label>
        <input aria-describedby={errors.password ? "password-error" : undefined} aria-invalid={errors.password ? true : undefined} id="password" type="password" autoComplete="new-password" disabled={pending} onChange={(event) => setPassword(event.target.value)} ref={passwordRef} value={password} />
        {errors.password && <p className="field-error" id="password-error">{errors.password}</p>}
        <label htmlFor="confirmation">Confirm password</label>
        <input aria-describedby={errors.confirmation ? "confirmation-error" : undefined} aria-invalid={errors.confirmation ? true : undefined} id="confirmation" type="password" autoComplete="new-password" disabled={pending} onChange={(event) => setConfirmation(event.target.value)} ref={confirmationRef} value={confirmation} />
        {errors.confirmation && <p className="field-error" id="confirmation-error">{errors.confirmation}</p>}
        <button disabled={pending} type="submit">{pending ? "Creating administrator…" : "Create administrator"}</button>
      </form>
    </section>
  </main>;
}
