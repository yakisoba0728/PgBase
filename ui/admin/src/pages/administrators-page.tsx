import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend, useSession } from "../auth/session";
import { AdministratorError, type AdministratorPage, type AdministratorProfile } from "../backend/ports";

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

  return <section>
    <div className="page-heading"><h2>Administrators</h2><button type="button" disabled={busy} onClick={() => load()}>Refresh</button></div>
    <p>Administrators have full access to this application. Your own account cannot be deactivated here.</p>
    {error && <p role="alert">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    <form aria-label="Create administrator" onSubmit={(event) => { event.preventDefault(); create(); }}>
      <fieldset disabled={busy}><legend>Create administrator</legend>
        <label>Email <input type="email" required autoComplete="off" value={email} onChange={(event) => setEmail(event.target.value)} /></label>
        <label>Password <input type="password" required autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>
        <p>Use 12–1024 UTF-8 bytes. This sets a password directly; no invitation email is sent.</p>
        <button type="submit">Create administrator</button>
      </fieldset>
    </form>
    {target && <form aria-label={`Set password for ${target.email}`} onSubmit={(event) => { event.preventDefault(); reset(); }}>
      <fieldset disabled={busy}><legend>Set password for {target.email}</legend>
        <p>The replacement revokes every session for this administrator. No reset email is sent.{target.id === ownID && " You will be signed out immediately."}</p>
        <label>New password <input type="password" required autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /></label>
        <button type="submit">Save password</button><button type="button" onClick={() => { setTarget(null); setNewPassword(""); }}>Cancel</button>
      </fieldset>
    </form>}
    {page && <>
      <div className="table-scroll" role="region" aria-label="Administrator accounts" tabIndex={0}><table className="users-table">
        <thead><tr><th>Email</th><th>Status</th><th>Created</th><th>Actions</th></tr></thead>
        <tbody>{page.items.map((account) => <tr key={account.id}>
          <td>{account.email}{account.id === ownID && " (you)"}</td><td>{account.active ? "Active" : "Inactive"}</td><td><time dateTime={account.createdAt}>{new Date(account.createdAt).toLocaleString()}</time></td>
          <td><button type="button" disabled={busy || account.id === ownID} onClick={() => changeActive(account)}>{account.active ? "Deactivate" : "Activate"}</button><button type="button" disabled={busy} onClick={() => { setTarget(account); setNewPassword(""); }}>Set password</button><button type="button" disabled={busy} onClick={() => revoke(account)}>Revoke sessions</button></td>
        </tr>)}</tbody>
      </table></div>
      {page.items.length === 0 && <p>No administrators on this page.</p>}
      <button type="button" disabled={busy} onClick={() => load()}>First page</button><button type="button" disabled={busy || !page.nextAfter} onClick={() => load(page.nextAfter)}>Next page</button>
    </>}
  </section>;
}
