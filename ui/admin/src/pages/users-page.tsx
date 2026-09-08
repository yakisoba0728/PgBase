import { useCallback, useEffect, useRef, useState } from "react";
import type { ManagedUser, UserPage } from "../backend/ports";
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
  const [pending, setPending] = useState(false);
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

  const busy = pending || loading;
  return <section>
    <div className="page-heading">
      <h2>Users</h2>
      <button type="button" onClick={() => load()} disabled={busy}>Refresh</button>
    </div>
    {error && <p role="alert">{error}</p>}
    {notice && <p role="status">{notice}</p>}
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
          </div></td>
        </tr>)}</tbody>
      </table></div>
      {page.items.length === 0 && <p>No public users yet.</p>}
      {page.nextAfter && <button type="button" disabled={busy} onClick={() => load(page.nextAfter)}>Next page</button>}
    </>}
  </section>;
}
