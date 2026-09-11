import { useCallback, useEffect, useRef, useState } from "react";
import type { ManagedUser, OAuthIdentity, UserPage } from "../backend/ports";
import { useBackend } from "../auth/session";

function displayDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function UsersPage() {
  const { users } = useBackend();
  const [page, setPage] = useState<UserPage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [impersonation, setImpersonation] = useState<{ token: string; email: string; expiresAt: string } | null>(null);
  const [pending, setPending] = useState(false);
	const [identities, setIdentities] = useState<{ userID: string; items: OAuthIdentity[] } | null>(null);
  const [loading, setLoading] = useState(true);
  const lifetime = useRef(0);
  const request = useRef(0);
  const pendingRef = useRef(false);
  const listController = useRef<AbortController | null>(null);

  const load = useCallback((after?: string) => {
    if (pendingRef.current) return;
    listController.current?.abort();
    const controller = new AbortController();
    listController.current = controller;
    const issued = ++request.current;
    const generation = lifetime.current;
    const current = () => !controller.signal.aborted && generation === lifetime.current && issued === request.current;
    setError(null);
    setNotice(null);
    setLoading(true);
    void users.list(after, controller.signal).then((value) => {
      if (current()) setPage(value);
    }).catch(() => {
      if (current()) setError("Unable to load users.");
    }).finally(() => {
      if (current()) {
        listController.current = null;
        setLoading(false);
      }
    });
  }, [users]);

  useEffect(() => {
    lifetime.current += 1;
    pendingRef.current = false;
    setPending(false);
    setPage(null);
    load();
    return () => {
      lifetime.current += 1;
      request.current += 1;
      listController.current?.abort();
      listController.current = null;
    };
  }, [load]);

  // A ref closes the gap before React renders disabled controls. The lifetime
  // guard also rejects successful mutations from an earlier mounted backend.
  async function mutate<T>(action: () => Promise<T>, commit: (value: T) => void, failure: string) {
    if (pendingRef.current || listController.current !== null) return;
    pendingRef.current = true;
    const generation = lifetime.current;
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      const result = await action();
      if (generation === lifetime.current) commit(result);
    } catch {
      if (generation === lifetime.current) setError(failure);
    } finally {
      if (generation === lifetime.current) {
        pendingRef.current = false;
        setPending(false);
      }
    }
  }

  function change(user: ManagedUser, active: boolean) {
    if (pendingRef.current) return;
    if (!active && !window.confirm(`Deactivate ${user.email}? All their sessions will be revoked.`)) return;
    void mutate(() => users.setActive(user.id, active), (updated) => {
      setPage((current) => current && {
        ...current,
        items: current.items.map((item) => item.id === updated.id ? updated : item),
      });
      setNotice(`${updated.email} is now ${updated.active ? "active" : "inactive"}.`);
    }, "Unable to update user. Try again.");
  }

  function revoke(user: ManagedUser) {
    if (pendingRef.current || !window.confirm(`Revoke all sessions for ${user.email}?`)) return;
    void mutate(() => users.revokeSessions(user.id), (count) => {
      setNotice(`Revoked ${count} stored sessions for ${user.email}.`);
    }, "Unable to revoke sessions. Try again.");
  }

  function verification(user: ManagedUser, verified: boolean) {
    void mutate(() => users.setVerified(user.id, verified), (updated) => {
      setPage((current) => current && ({ ...current, items: current.items.map((item) => item.id === updated.id ? updated : item) }));
      setNotice(`${updated.email} is now ${updated.verified ? "verified" : "unverified"}.`);
    }, "Unable to update verification state.");
  }

  function sendAction(user: ManagedUser, kind: "verification" | "reset") {
    const action = kind === "verification" ? users.sendVerification(user.id) : users.sendPasswordReset(user.id);
    void mutate(() => action, () => setNotice(`${kind === "verification" ? "Verification" : "Password reset"} email requested for ${user.email}.`), "Unable to request email action.");
  }

  function impersonate(user: ManagedUser) {
    if (!window.confirm(`Create a five-minute impersonation session for ${user.email}?`)) return;
    void mutate(() => users.impersonate(user.id, 300), (session) => {
      setImpersonation({ token: session.token, email: user.email, expiresAt: session.expiresAt });
      setNotice(`Created an impersonation session for ${user.email}.`);
    }, "Unable to create impersonation session.");
  }
	function loadIdentities(user: ManagedUser) {
		if (!users.oauthIdentities) return; void mutate(() => users.oauthIdentities!(user.id), (items) => setIdentities({ userID: user.id, items }), "Unable to load linked OAuth providers.");
	}
	function unlinkIdentity(user: ManagedUser, provider: string) {
		if (!users.unlinkOAuthIdentity || !window.confirm(`Unlink ${provider} from ${user.email}? The server will refuse if this is the final sign-in method.`)) return;
		void mutate(() => users.unlinkOAuthIdentity!(user.id, provider), () => { setIdentities(current => current?.userID === user.id ? { ...current, items: current.items.filter(identity => identity.provider !== provider) } : current); setNotice(`${provider} was unlinked from ${user.email}.`); }, "Unable to unlink this provider. It may be the user's final sign-in method.");
	}

  const busy = pending || loading;
  return <section>
    <div className="page-heading">
      <h2>Users</h2>
      <button type="button" onClick={() => load()} disabled={busy}>Refresh</button>
    </div>
    {error && <p role="alert">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {impersonation && <div role="region" aria-label="Impersonation session">
      <p>This temporary token acts as {impersonation.email} until {displayDate(impersonation.expiresAt)}. Credential and MFA changes are blocked.</p>
      <textarea readOnly aria-label="Impersonation token" value={impersonation.token} rows={3} />
      <button type="button" onClick={() => void navigator.clipboard.writeText(impersonation.token)}>Copy token</button>
      <button type="button" onClick={() => setImpersonation(null)}>Dismiss</button>
    </div>}
    {loading && <p aria-live="polite">Loading users…</p>}
    {page && <>
      <div className="table-scroll" role="region" aria-label="Public users" tabIndex={0}><table className="users-table">
        <thead><tr><th>User</th><th>Status</th><th>Dates</th><th>Actions</th></tr></thead>
        <tbody>{page.items.map((user) => <tr key={user.id}>
          <td><span className="user-identity">{user.email}</span><small className="user-identity">{user.id}</small></td>
          <td>{user.active ? "Active" : "Inactive"}<br /><small>{user.verified ? "Verified" : "Unverified"}</small></td>
          <td>
            <small>Created</small><time dateTime={user.createdAt}>{displayDate(user.createdAt)}</time>
            <small>Updated</small><time dateTime={user.updatedAt}>{displayDate(user.updatedAt)}</time>
          </td>
          <td><div className="user-actions">
            <button type="button" disabled={busy} onClick={() => change(user, !user.active)}>{user.active ? "Deactivate" : "Activate"}</button>
            <button type="button" disabled={busy} onClick={() => revoke(user)}>Revoke sessions</button>
            <button type="button" disabled={busy} onClick={() => verification(user, !user.verified)}>{user.verified ? "Mark unverified" : "Mark verified"}</button>
            {!user.verified && <button type="button" disabled={busy} onClick={() => sendAction(user, "verification")}>Send verification</button>}
            <button type="button" disabled={busy} onClick={() => sendAction(user, "reset")}>Send password reset</button>
			{users.oauthIdentities && <button type="button" disabled={busy} onClick={() => loadIdentities(user)}>OAuth providers</button>}
            <button type="button" disabled={busy || !user.active} onClick={() => impersonate(user)}>Impersonate</button>
			{identities?.userID === user.id && <div aria-label={`OAuth providers for ${user.email}`}><ul>{identities.items.map(identity => <li key={`${identity.provider}:${identity.subject}`}>{identity.provider} · {identity.subject} <button type="button" disabled={busy || !users.unlinkOAuthIdentity} onClick={() => unlinkIdentity(user, identity.provider)}>Unlink {identity.provider}</button></li>)}</ul>{identities.items.length === 0 && <p>No OAuth providers linked.</p>}</div>}
          </div></td>
        </tr>)}</tbody>
      </table></div>
      {page.items.length === 0 && <p>No public users yet.</p>}
      {page.nextAfter && <button type="button" disabled={busy} onClick={() => load(page.nextAfter)}>Next page</button>}
    </>}
  </section>;
}
