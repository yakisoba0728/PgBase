import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "../components/ui";
import { PasswordInput } from "../components/password-input";
import { sessionFailure } from "../auth/session";
import type { SetupBackupJob, SetupInput } from "../backend/ports";

interface SetupPageProps {
  onSetup(input: SetupInput): Promise<void>;
	onUploadBackup?(setupToken: string, archive: File): Promise<{ id: string }>;
	onRestoreBackup?(setupToken: string, id: string): Promise<unknown>;
	onSetupBackupJob?(setupToken: string, id: string, signal?: AbortSignal): Promise<SetupBackupJob>;
}

type SetupField = "setupToken" | "email" | "password" | "confirmation";
type SetupErrors = Partial<Record<SetupField | "form", string>>;

// Administrator setup errors carry a code and no field details
// (writeAdminAuthError never fills Details), so every server message is keyed by
// code alone and the byte bounds are enforced here before the request is sent.
const minimumPasswordBytes = 12;
const maximumPasswordBytes = 1024;

function passwordBytes(password: string): number {
  return new TextEncoder().encode(password).length;
}

function safeServerErrors(error: unknown): SetupErrors {
  const reason = sessionFailure(error);
  if (reason === "invalid-credentials") return { setupToken: "Setup token is invalid." };
  if (reason === "setup-unavailable") return { form: "Administrator setup is no longer available." };
  if (reason === "invalid-input") return { form: "Enter a valid email address and a password of 12 to 1024 bytes." };
  return { form: "Unable to create the administrator. Try again." };
}

export function SetupPage({ onSetup, onUploadBackup, onRestoreBackup, onSetupBackupJob }: SetupPageProps) {
  const [setupToken, setSetupToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [showSetupToken, setShowSetupToken] = useState(false);
  const [errors, setErrors] = useState<SetupErrors>({});
  const [pending, setPending] = useState(false);
  const [restoreLocked, setRestoreLocked] = useState(false);
	const [backup, setBackup] = useState<{ id: string; name: string } | null>(null);
	const [backupMessage, setBackupMessage] = useState("");
	const [restoreJob, setRestore] = useState<{ token: string; jobID: string } | null>(null);
	useEffect(() => {
		if (!restoreJob || !onSetupBackupJob) return;
		const controller = new AbortController(); let inFlight = false; const timer = window.setInterval(() => { if (inFlight) return; inFlight = true; void onSetupBackupJob(restoreJob.token, restoreJob.jobID, controller.signal).then(job => { if (controller.signal.aborted) return; if (job.status === "failed") { setBackupMessage(job.error || "Backup restore failed."); setRestore(null); setRestoreLocked(false); } else if (job.status === "completed") { setBackupMessage("Restore completed. The replacement database is activating; reload this page when it is ready."); setRestore(null); } }).catch(() => { if (!controller.signal.aborted) setBackupMessage("Restore status is temporarily unavailable. Do not create an administrator until it finishes."); }).finally(() => { inFlight = false; }); }, 1000);
		return () => { controller.abort(); window.clearInterval(timer); };
	}, [onSetupBackupJob, restoreJob]);
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
    if (pending || restoreLocked) return;
    const nextErrors: SetupErrors = {};
    if (!setupToken.trim()) nextErrors.setupToken = "Setup token is required.";
    if (!email.trim()) nextErrors.email = "Email is required.";
    const bytes = passwordBytes(password);
    if (!password) nextErrors.password = "Password is required.";
    else if (bytes < minimumPasswordBytes || bytes > maximumPasswordBytes) {
      nextErrors.password = `Password must be ${minimumPasswordBytes} to ${maximumPasswordBytes} bytes.`;
    }
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
      setErrors(safeServerErrors(error));
    } finally {
      setPending(false);
    }
  }
  async function restore(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const archive = (event.currentTarget.elements.namedItem("setup-backup") as HTMLInputElement | null)?.files?.[0];
    if (!onUploadBackup || !onRestoreBackup || !archive || !setupToken.trim() || pending || restoreLocked) {
      setBackupMessage("Enter the setup token and select a gzip backup."); return;
    }
    if (!window.confirm("Restore this backup before creating the first administrator? This replaces the initial database state.")) return;
    setPending(true); setBackupMessage("");
    let dispatched = false;
    try {
      const uploaded = await onUploadBackup(setupToken, archive);
      setBackup({ id: uploaded.id, name: archive.name });
      dispatched = true;
      setRestoreLocked(true);
      const job = await onRestoreBackup(setupToken, uploaded.id) as SetupBackupJob;
      if (onSetupBackupJob) setRestore({ token: setupToken, jobID: job.id });
      setBackupMessage(onSetupBackupJob ? "Restore started. First-administrator creation is locked until the restore is confirmed." : "Restore started. Wait for the replacement database to activate, then reload this page.");
    } catch {
      setBackupMessage(dispatched ? "Restore outcome is unknown. Do not create an administrator. Reload when the replacement database is ready to check its state." : "Backup upload failed. Verify the setup token and archive before trying again.");
    } finally { setPending(false); }
  }

  return <main className="auth-page setup-page">
    <div className="login-content">
    <section aria-labelledby="setup-title" className="login-panel setup-panel">
      <div className="auth-brand"><span className="auth-brand-mark" aria-hidden="true">pB</span><span>pgBase</span></div>
      <p className="login-context">Welcome to your backend</p>
      <h1 id="setup-title">Set up pgBase</h1>
      <p className="auth-description">Create the first administrator for this pgBase instance.</p>
      <form aria-busy={pending} noValidate onSubmit={submit}>
        {errors.form && <p className="form-error login-feedback" id="setup-form-error" ref={formErrorRef} role="alert" tabIndex={-1}>{errors.form}</p>}
        <div className="login-field">
          <label htmlFor="setup-token">Setup token</label>
          <div className="password-input">
            <input aria-describedby={errors.setupToken ? "setup-token-hint setup-token-error" : "setup-token-hint"} aria-invalid={errors.setupToken ? true : undefined} id="setup-token" type={showSetupToken ? "text" : "password"} autoComplete="off" spellCheck={false} disabled={pending || restoreLocked} onChange={event => setSetupToken(event.target.value)} ref={setupTokenRef} value={setupToken} />
            <Button aria-controls="setup-token" aria-label={showSetupToken ? "Hide setup token" : "Show setup token"} aria-pressed={showSetupToken} className="password-toggle" disabled={pending || restoreLocked} onClick={() => setShowSetupToken(visible => !visible)} variant="ghost">{showSetupToken ? "Hide" : "Show"}</Button>
          </div>
          <p id="setup-token-hint">Use the setup token configured for this instance.</p>
          {errors.setupToken && <p className="field-error" id="setup-token-error">{errors.setupToken}</p>}
        </div>
        <div className="login-field">
          <label htmlFor="email">Email</label>
          <input aria-describedby={errors.email ? "email-error" : undefined} aria-invalid={errors.email ? true : undefined} id="email" type="email" autoCapitalize="none" spellCheck={false} autoComplete="email" disabled={pending || restoreLocked} onChange={event => setEmail(event.target.value)} ref={emailRef} value={email} placeholder="name@example.com" />
          {errors.email && <p className="field-error" id="email-error">{errors.email}</p>}
        </div>
        <div className="login-field">
          <label htmlFor="password">Password</label>
          <PasswordInput aria-describedby={errors.password ? "password-hint password-error" : "password-hint"} aria-invalid={errors.password ? true : undefined} id="password" autoComplete="new-password" disabled={pending || restoreLocked} minLength={minimumPasswordBytes} onChange={event => setPassword(event.target.value)} ref={passwordRef} value={password} />
          <p id="password-hint">Passwords must be 12 to 1024 bytes; non-ASCII characters count as more than one byte.</p>
          {errors.password && <p className="field-error" id="password-error">{errors.password}</p>}
        </div>
        <div className="login-field">
          <label htmlFor="confirmation">Confirm password</label>
          <input aria-describedby={errors.confirmation ? "confirmation-error" : undefined} aria-invalid={errors.confirmation ? true : undefined} id="confirmation" type="password" autoComplete="new-password" disabled={pending || restoreLocked} onChange={event => setConfirmation(event.target.value)} ref={confirmationRef} value={confirmation} />
          {errors.confirmation && <p className="field-error" id="confirmation-error">{errors.confirmation}</p>}
        </div>
        <Button disabled={pending || restoreLocked} type="submit">{restoreLocked ? "Waiting for backup restore…" : pending ? "Creating administrator…" : "Create administrator"}</Button>
      </form>
      {onUploadBackup && onRestoreBackup && <details className="setup-backup">
        <summary>Restore an initial backup</summary>
        <p>Restore an existing project instead of creating an administrator. This replaces the initial database and may restart this instance.</p>
        <form aria-busy={pending || restoreLocked} onSubmit={restore}>
          <fieldset disabled={pending || restoreLocked}><legend>Backup archive</legend><label htmlFor="setup-backup">Gzip backup</label><input accept=".gz,application/gzip" id="setup-backup" name="setup-backup" type="file" /><Button variant="secondary" type="submit">Upload and restore backup</Button></fieldset>
        </form>
        {backup && <p>Uploaded {backup.name}.</p>}
      </details>}
      {backupMessage && <p className="login-notice" role="status">{backupMessage}</p>}
      <p className="auth-footer">Your administrator account has full access to this project.</p>
    </section>
    </div>
  </main>;
}
