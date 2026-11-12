import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend, useSession } from "../auth/session";
import { AdministratorError, type AdministratorPage, type AdministratorProfile } from "../backend/ports";
import { Button, FormSection, PageHeading } from "../components/ui";
import { PasswordInput } from "../components/password-input";

export function AdministratorsPage() {
  const { administrators } = useBackend();
  const { state } = useSession();
  const [page, setPage] = useState<AdministratorPage | null>(null);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [target, setTarget] = useState<AdministratorProfile | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const request = useRef<AbortController | null>(null);
  const ownID = state.kind === "authenticated" ? state.administrator.id : "";

  const run = useCallback((work: (signal: AbortSignal) => Promise<void>, success?: string) => {
    if (request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setBusy(true); setError(null); setNotice(null);
    const timer = window.setTimeout(() => {
      controller.abort();
      setError("Unable to confirm the request. Refresh the account list before retrying.");
      request.current = null; setBusy(false);
    }, 10_000);
    controller.signal.addEventListener("abort", () => window.clearTimeout(timer), { once: true });
    void work(controller.signal).then(() => {
      if (!controller.signal.aborted && success) setNotice(success);
    }).catch((caught: unknown) => {
      if (controller.signal.aborted) return;
      const reason = caught instanceof AdministratorError ? caught.reason : "failed";
      setError(reason === "conflict"
        ? "The email already exists, or this change would disable your own account or the last active administrator. Refresh and try again."
        : reason === "invalid" ? "Check the email and password. Passwords must contain 12–1024 UTF-8 bytes."
          : reason === "not-found" ? "This administrator no longer exists. Refresh the account list."
            : "Unable to complete the administrator request. Refresh before retrying.");
    }).finally(() => {
      window.clearTimeout(timer);
      if (request.current === controller) { request.current = null; setBusy(false); }
    });
  }, []);

  const load = useCallback((after?: string) => {
    if (!administrators) return;
    run(async (signal) => {
      const next = await administrators.list(after, signal);
      if (!signal.aborted) { setPage(next); setTarget(null); setNewPassword(""); }
    });
  }, [administrators, run]);

  useEffect(() => {
    load();
    return () => { request.current?.abort(); request.current = null; };
  }, [load]);

  if (!administrators) return <section><h2>Administrators</h2><p role="alert">Administrator account management is unavailable.</p></section>;

  function create() {
    if (request.current) return;
    const supplied = password; setPassword("");
    run(async (signal) => {
      await administrators!.create({ email: email.trim(), password: supplied }, signal);
      if (signal.aborted) return;
      setEmail("");
      const next = await administrators!.list(undefined, signal);
      if (!signal.aborted) setPage(next);
    }, "Administrator created. Share the password through your usual secure channel.");
  }

  function changeActive(account: AdministratorProfile) {
    if (request.current || account.id === ownID) return;
    if (account.active && !window.confirm(`Deactivate ${account.email}? All their sessions will be revoked.`)) return;
    run(async (signal) => {
      const updated = await administrators!.setActive(account.id, !account.active, signal);
      if (!signal.aborted) setPage((current) => current && { ...current, items: current.items.map((item) => item.id === updated.id ? updated : item) });
    }, `${account.email} is now ${account.active ? "inactive" : "active"}.`);
  }

  function revoke(account: AdministratorProfile) {
    if (request.current || !window.confirm(`Revoke all sessions for ${account.email}?${account.id === ownID ? " You will be signed out immediately." : ""}`)) return;
    run((signal) => administrators!.revokeSessions(account.id, signal), `All sessions revoked for ${account.email}.`);
  }

  function reset() {
    if (!target || request.current) return;
    if (target.id === ownID && !window.confirm("Replace your password and revoke all your sessions? You will be signed out immediately.")) return;
    const supplied = newPassword; setNewPassword("");
    run(async (signal) => {
      await administrators!.resetPassword(target.id, supplied, signal);
      if (!signal.aborted) setTarget(null);
    }, `Password replaced for ${target.email}. All their sessions were revoked.`);
  }

  return <section className="access-page administrators-page">
    <PageHeading title="Administrators" description="Administrators have full access to this application. Your own account cannot be deactivated here." actions={<Button disabled={busy} onClick={() => load()} variant="secondary">Refresh</Button>} />
    {error && <p className="access-feedback form-error" role="alert">{error}</p>}
    {notice && <p className="access-feedback" role="status">{notice}</p>}
    <form aria-label="Create administrator" onSubmit={(event) => { event.preventDefault(); create(); }}>
      <FormSection disabled={busy} title="Create administrator">
        <div className="access-form-grid">
          <label className="access-field">Email <input type="email" required autoComplete="off" placeholder="name@example.com" value={email} onChange={(event) => setEmail(event.target.value)} /></label>
          <div className="access-field"><label htmlFor="administrator-password">Password</label><PasswordInput disabled={busy} id="administrator-password" aria-describedby="administrator-password-help" required autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} /></div>
        </div>
        <p className="access-help" id="administrator-password-help">Use 12–1024 UTF-8 bytes. This sets a password directly; no invitation email is sent.</p>
        <div className="access-form-actions"><Button type="submit">Create administrator</Button></div>
      </FormSection>
    </form>
    {target && <form aria-label={`Set password for ${target.email}`} onSubmit={(event) => { event.preventDefault(); reset(); }}>
      <FormSection disabled={busy} title={`Set password for ${target.email}`}>
        <p className="access-help" id="administrator-reset-help">The replacement revokes every session for this administrator. No reset email is sent.{target.id === ownID && " You will be signed out immediately."}</p>
        <div className="access-field"><label htmlFor="administrator-new-password">New password</label><PasswordInput disabled={busy} id="administrator-new-password" aria-describedby="administrator-reset-help" required autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /></div>
        <div className="access-form-actions"><Button onClick={() => { setTarget(null); setNewPassword(""); }} variant="secondary">Cancel</Button><Button type="submit">Save password</Button></div>
      </FormSection>
    </form>}
    {page && <>
      <div className="access-list-heading"><h3>Administrator accounts</h3><p>Manage account status, passwords, and active sessions.</p></div>
      <div className="table-scroll" role="region" aria-label="Administrator accounts" tabIndex={0}><table className="users-table">
        <thead><tr><th>Email</th><th>Status</th><th>Created</th><th>Actions</th></tr></thead>
        <tbody>{page.items.map((account) => <tr key={account.id}>
          <td>{account.email}{account.id === ownID && " (you)"}</td><td>{account.active ? "Active" : "Inactive"}</td><td><time dateTime={account.createdAt}>{new Date(account.createdAt).toLocaleString()}</time></td>
          <td><div className="access-item-actions"><Button disabled={busy || account.id === ownID} onClick={() => changeActive(account)} variant="secondary">{account.active ? "Deactivate" : "Activate"}</Button><Button disabled={busy} onClick={() => { setTarget(account); setNewPassword(""); }} variant="secondary">Set password</Button><Button disabled={busy} onClick={() => revoke(account)} variant="danger">Revoke sessions</Button></div></td>
        </tr>)}</tbody>
      </table></div>
      {page.items.length === 0 && <p>No administrators on this page.</p>}
      <nav aria-label="Administrator pagination" className="access-pagination"><Button disabled={busy} onClick={() => load()} variant="secondary">First page</Button><Button disabled={busy || !page.nextAfter} onClick={() => load(page.nextAfter)} variant="secondary">Next page</Button></nav>
    </>}
  </section>;
}
