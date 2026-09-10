import { useEffect, useRef, useState, type FormEvent } from "react";
import { sessionFailure } from "../auth/session";
import type { SetupBackupJob, SetupInput } from "../backend/ports";

interface SetupPageProps {
  onSetup(input: SetupInput): Promise<void>;
	onUploadBackup?(setupToken: string, archive: File): Promise<{ id: string }>;
	onRestoreBackup?(setupToken: string, id: string): Promise<unknown>;
	onSetupBackupJob?(setupToken: string, id: string): Promise<SetupBackupJob>;
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
	const [backup, setBackup] = useState<{ id: string; name: string } | null>(null);
	const [backupMessage, setBackupMessage] = useState("");
	const [restoreJob, setRestore] = useState<{ token: string; jobID: string } | null>(null);
	useEffect(() => {
		if (!restoreJob || !onSetupBackupJob) return;
		const controller = new AbortController(); const timer = window.setInterval(() => void onSetupBackupJob(restoreJob.token, restoreJob.jobID).then(job => { if (job.status === "failed") { setBackupMessage(job.error || "Backup restore failed."); setRestore(null); } else if (job.status === "completed") { setBackupMessage("Restore completed. The replacement database is activating; reload this page when it is ready."); setRestore(null); } }).catch(() => { if (!controller.signal.aborted) setBackupMessage("Restore status is temporarily unavailable. Do not create an administrator until it finishes."); }), 1000);
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
		event.preventDefault(); const archive = (event.currentTarget.elements.namedItem("setup-backup") as HTMLInputElement | null)?.files?.[0];
		if (!onUploadBackup || !onRestoreBackup || !archive || !setupToken.trim() || pending) { setBackupMessage("Enter the setup token and select a gzip backup."); return; }
		if (!window.confirm("Restore this backup before creating the first administrator? This replaces the initial database state.")) return;
		setPending(true); setBackupMessage(""); try { const uploaded = await onUploadBackup(setupToken, archive); setBackup({ id: uploaded.id, name: archive.name }); const job = await onRestoreBackup(setupToken, uploaded.id) as SetupBackupJob; if (onSetupBackupJob) setRestore({ token: setupToken, jobID: job.id }); setBackupMessage(onSetupBackupJob ? "Restore started. First-administrator creation is locked until the restore is confirmed." : "Restore started. Wait for the replacement database to activate, then reload this page."); } catch { setBackupMessage("Backup restore could not be started. Verify the setup token and archive."); } finally { setPending(false); }
	}

  return <main className="auth-page">
    <section aria-labelledby="setup-title" className="auth-card">
      <h1 id="setup-title">Set up pgBase</h1>
      <p>Create the first administrator for this pgBase instance.</p>
		{onUploadBackup && onRestoreBackup && <form onSubmit={restore}><fieldset disabled={pending || restoreJob !== null}><legend>Restore an initial backup</legend><p>Available only before the first administrator exists. Restore starts before account creation and may restart this instance.</p><label htmlFor="setup-backup">Gzip backup</label><input accept=".gz,application/gzip" id="setup-backup" name="setup-backup" type="file" /><button type="submit">Upload and restore backup</button>{backup && <p>Uploaded {backup.name}.</p>}{backupMessage && <p role="status">{backupMessage}</p>}</fieldset></form>}
      <form noValidate onSubmit={submit}>
        {errors.form && <p className="form-error" id="setup-form-error" ref={formErrorRef} role="alert" tabIndex={-1}>{errors.form}</p>}
        <label htmlFor="setup-token">Setup token</label>
        <input aria-describedby={errors.setupToken ? "setup-token-error" : undefined} aria-invalid={errors.setupToken ? true : undefined} id="setup-token" type={showSetupToken ? "text" : "password"} autoComplete="off" disabled={pending || restoreJob !== null} onChange={(event) => setSetupToken(event.target.value)} ref={setupTokenRef} value={setupToken} />
        <button aria-pressed={showSetupToken} disabled={pending} onClick={() => setShowSetupToken((visible) => !visible)} type="button">{showSetupToken ? "Hide setup token" : "Show setup token"}</button>
        {errors.setupToken && <p className="field-error" id="setup-token-error">{errors.setupToken}</p>}
        <label htmlFor="email">Email</label>
        <input aria-describedby={errors.email ? "email-error" : undefined} aria-invalid={errors.email ? true : undefined} id="email" type="email" autoComplete="email" disabled={pending} onChange={(event) => setEmail(event.target.value)} ref={emailRef} value={email} />
        {errors.email && <p className="field-error" id="email-error">{errors.email}</p>}
        <label htmlFor="password">Password</label>
        <input aria-describedby={errors.password ? "password-hint password-error" : "password-hint"} aria-invalid={errors.password ? true : undefined} id="password" type="password" autoComplete="new-password" disabled={pending} minLength={minimumPasswordBytes} onChange={(event) => setPassword(event.target.value)} ref={passwordRef} value={password} />
        <p id="password-hint">Passwords must be 12 to 1024 bytes; non-ASCII characters count as more than one byte.</p>
        {errors.password && <p className="field-error" id="password-error">{errors.password}</p>}
        <label htmlFor="confirmation">Confirm password</label>
        <input aria-describedby={errors.confirmation ? "confirmation-error" : undefined} aria-invalid={errors.confirmation ? true : undefined} id="confirmation" type="password" autoComplete="new-password" disabled={pending} onChange={(event) => setConfirmation(event.target.value)} ref={confirmationRef} value={confirmation} />
        {errors.confirmation && <p className="field-error" id="confirmation-error">{errors.confirmation}</p>}
        <button disabled={pending || restoreJob !== null} type="submit">{pending ? "Creating administrator…" : restoreJob ? "Waiting for backup restore…" : "Create administrator"}</button>
      </form>
    </section>
  </main>;
}
