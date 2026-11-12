import { t, message as bilingual, type Message, formatDate, useLocale } from "../i18n";
import { AuthRecordsPanel } from "./auth-records-panel";
import { Button, EmptyState, PageHeading } from "../components/ui";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ManagedUser, OAuthIdentity, UserPage } from "../backend/ports";
import { useBackend } from "../auth/session";

function displayDate(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : formatDate(date);
}

export function UsersPage() {
  useLocale();
  const { users, capabilities } = useBackend();
  const [page, setPage] = useState<UserPage | null>(null);
  const [error, setError] = useState<Message | null>(null);
  const [notice, setNotice] = useState<Message | null>(null);
  const [impersonation, setImpersonation] = useState<{ token: string; email: string; expiresAt: string } | null>(null);
  const [pending, setPending] = useState(false);
	const [identities, setIdentities] = useState<{ userID: string; items: OAuthIdentity[] } | null>(null);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
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
    if (!active && !window.confirm(t(bilingual(`Deactivate ${user.email}? All their sessions will be revoked.`, `${user.email} 사용자를 비활성화할까요? 모든 세션이 폐기됩니다.`)))) return;
    void mutate(() => users.setActive(user.id, active), (updated) => {
      setPage((current) => current && {
        ...current,
        items: current.items.map((item) => item.id === updated.id ? updated : item),
      });
      setNotice(bilingual(`${updated.email} is now ${updated.active ? "active" : "inactive"}.`, `${updated.email} 사용자를 ${updated.active ? "활성화" : "비활성화"}했습니다.`));
    }, "Unable to update user. Try again.");
  }

  function revoke(user: ManagedUser) {
    if (pendingRef.current || !window.confirm(t(bilingual(`Revoke all sessions for ${user.email}?`, `${user.email} 사용자의 모든 세션을 폐기할까요?`)))) return;
    void mutate(() => users.revokeSessions(user.id), (count) => {
      setNotice(bilingual(`Revoked ${count} stored sessions for ${user.email}.`, `${user.email} 사용자의 저장된 세션 ${count}개를 폐기했습니다.`));
    }, "Unable to revoke sessions. Try again.");
  }

  function verification(user: ManagedUser, verified: boolean) {
    void mutate(() => users.setVerified(user.id, verified), (updated) => {
      setPage((current) => current && ({ ...current, items: current.items.map((item) => item.id === updated.id ? updated : item) }));
      setNotice(bilingual(`${updated.email} is now ${updated.verified ? "verified" : "unverified"}.`, `${updated.email} 사용자를 ${updated.verified ? "인증됨" : "미인증"} 상태로 변경했습니다.`));
    }, "Unable to update verification state.");
  }

  function sendAction(user: ManagedUser, kind: "verification" | "reset") {
    void mutate(() => kind === "verification" ? users.sendVerification(user.id) : users.sendPasswordReset(user.id), () => setNotice(bilingual(`${kind === "verification" ? "Verification" : "Password reset"} email requested for ${user.email}.`, `${user.email} 사용자에게 ${kind === "verification" ? "인증" : "비밀번호 재설정"} 메일을 요청했습니다.`)), "Unable to request email action.");
  }

  function impersonate(user: ManagedUser) {
    if (!window.confirm(t(bilingual(`Create a five-minute impersonation session for ${user.email}?`, `${user.email} 사용자로 대행하는 5분 세션을 생성할까요?`)))) return;
    void mutate(() => users.impersonate(user.id, 300), (session) => {
      setImpersonation({ token: session.token, email: user.email, expiresAt: session.expiresAt });
      setNotice(bilingual(`Created an impersonation session for ${user.email}.`, `${user.email} 사용자의 대행 세션을 생성했습니다.`));
    }, "Unable to create impersonation session.");
  }
	function loadIdentities(user: ManagedUser) {
		if (!users.oauthIdentities) return; void mutate(() => users.oauthIdentities!(user.id), (items) => setIdentities({ userID: user.id, items }), "Unable to load linked OAuth providers.");
	}
	function unlinkIdentity(user: ManagedUser, provider: string) {
		if (!users.unlinkOAuthIdentity || !window.confirm(t(bilingual(`Unlink ${provider} from ${user.email}? The server will refuse if this is the final sign-in method.`, `${user.email} 사용자의 ${provider} 연결을 해제할까요? 마지막 로그인 수단이면 서버에서 거부합니다.`)))) return;
		void mutate(() => users.unlinkOAuthIdentity!(user.id, provider), () => { setIdentities(current => current?.userID === user.id ? { ...current, items: current.items.filter(identity => identity.provider !== provider) } : current); setNotice(bilingual(`${provider} was unlinked from ${user.email}.`, `${user.email} 사용자의 ${provider} 연결을 해제했습니다.`)); }, "Unable to unlink this provider. It may be the user's final sign-in method.");
	}

  const busy = pending || loading;
  const visibleUsers = page?.items.filter(user => `${user.email} ${user.id}`.toLowerCase().includes(query.trim().toLowerCase())) ?? [];
  return <section className="users-page management-page">
    <PageHeading title={t("Users")} description={t("Manage app users, verification and active sessions.")} actions={<Button variant="secondary" onClick={() => load()} disabled={busy}>{t("Refresh")}</Button>} />
    {error && <p role="alert">{t(error)}</p>}
    {notice && <p role="status">{t(notice)}</p>}
    {impersonation && <div role="region" aria-label={t("Impersonation session")}>
      <p>{t(bilingual(`This temporary token acts as ${impersonation.email} until ${displayDate(impersonation.expiresAt)}. Credential and MFA changes are blocked.`, `이 임시 토큰은 ${displayDate(impersonation.expiresAt)}까지 ${impersonation.email} 사용자로 동작합니다. 자격 증명과 MFA 변경은 차단됩니다.`))}</p>
      <textarea readOnly aria-label={t("Impersonation token")} value={impersonation.token} rows={3} />
      <Button variant="secondary" onClick={() => { if (!navigator.clipboard) { setError("Clipboard is unavailable. Select and copy the token manually."); return; } void navigator.clipboard.writeText(impersonation.token).then(() => setNotice("Impersonation token copied.")).catch(() => setError("Unable to copy the token. Select and copy it manually.")); }}>{t("Copy token")}</Button>
      <Button variant="secondary" onClick={() => setImpersonation(null)}>{t("Dismiss")}</Button>
    </div>}
    {loading && <p aria-live="polite">{t("Loading users…")}</p>}
    {page && <>
      <div className="access-search-toolbar"><label>{t("Search loaded users")}<input type="search" value={query} placeholder={t("Email or user ID…")} onChange={event => setQuery(event.target.value)} /></label><span className="settings-help">{t(bilingual(`${visibleUsers.length} of ${page.items.length} on this page`, `이 페이지 ${page.items.length}명 중 ${visibleUsers.length}명`))}</span></div>
      <div className="table-scroll" role="region" aria-label={t("Public users")} tabIndex={0}><table className="users-table">
        <thead><tr><th>{t("User")}</th><th>{t("Status")}</th><th>{t("Dates")}</th><th>{t("Actions")}</th></tr></thead>
        <tbody>{visibleUsers.map((user) => <tr key={user.id}>
          <td><span className="user-identity">{user.email}</span><small className="user-identity">{user.id}</small></td>
          <td><span className="status-badge" data-status={user.active ? "active" : "inactive"}>{user.active ? t("Active") : t("Inactive")}</span><br /><small>{user.verified ? t("Verified") : t("Unverified")}</small></td>
          <td>
            <small>{t("Created")}</small><time dateTime={user.createdAt}>{displayDate(user.createdAt)}</time>
            <small>{t("Updated")}</small><time dateTime={user.updatedAt}>{displayDate(user.updatedAt)}</time>
          </td>
          <td><div className="user-actions">
            <Button variant="secondary" disabled={busy} onClick={() => change(user, !user.active)}>{user.active ? t("Deactivate") : t("Activate")}</Button>
            <Button variant="secondary" disabled={busy} onClick={() => revoke(user)}>{t("Revoke sessions")}</Button>
            {capabilities?.authAdministration !== false && <Button variant="secondary" disabled={busy} onClick={() => verification(user, !user.verified)}>{user.verified ? t("Mark unverified") : t("Mark verified")}</Button>}
            {capabilities?.authAdministration !== false && !user.verified && <Button variant="secondary" disabled={busy} onClick={() => sendAction(user, "verification")}>{t("Send verification")}</Button>}
            {capabilities?.authAdministration !== false && <Button variant="secondary" disabled={busy} onClick={() => sendAction(user, "reset")}>{t("Send password reset")}</Button>}
			{capabilities?.authAdministration !== false && users.oauthIdentities && <Button variant="secondary" disabled={busy} onClick={() => loadIdentities(user)}>{t("OAuth providers")}</Button>}
            <Button variant="secondary" disabled={busy || !user.active} onClick={() => impersonate(user)}>{t("Impersonate")}</Button>
			{capabilities?.authAdministration !== false && identities?.userID === user.id && <div aria-label={t(bilingual(`OAuth providers for ${user.email}`, `${user.email} 사용자의 OAuth 제공자`))}><ul>{identities.items.map(identity => <li key={`${identity.provider}:${identity.subject}`}>{identity.provider} · {identity.subject} <Button variant="secondary" disabled={busy || !users.unlinkOAuthIdentity} onClick={() => unlinkIdentity(user, identity.provider)}>{t(bilingual(`Unlink ${identity.provider}`, `${identity.provider} 연결 해제`))}</Button></li>)}</ul>{identities.items.length === 0 && <p>{t("No OAuth providers linked.")}</p>}</div>}
          </div></td>
        </tr>)}</tbody>
      </table></div>
      {page.items.length > 0 && visibleUsers.length === 0 && <p role="status">{t(bilingual(`No users match “${query}” on this page.`, `이 페이지에 “${query}”와 일치하는 사용자가 없습니다.`))}</p>}
      {page.items.length === 0 && <EmptyState title={t("No public users yet.")} description={t("App users will appear here when they register through an auth collection.")} />}
      {page.nextAfter && <Button variant="secondary" disabled={busy} onClick={() => load(page.nextAfter)}>{t("Next page")}</Button>}
    </>}
  <AuthRecordsPanel /></section>;
}
