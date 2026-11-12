import { t, message as bilingual, type Message, formatDate, useLocale } from "../i18n";
import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend, useSession } from "../auth/session";
import { AdministratorError, type AdministratorPage, type AdministratorProfile } from "../backend/ports";
import { Button, FormSection, PageHeading } from "../components/ui";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import { PasswordInput } from "../components/password-input";

export function AdministratorsPage() {
  useLocale();
  const { administrators } = useBackend();
  const { state } = useSession();
  const [page, setPage] = useState<AdministratorPage | null>(null);
  const [email, setEmail] = useState("");
  const [query, setQuery] = useState("");
  const [password, setPassword] = useState("");
  const [target, setTarget] = useState<AdministratorProfile | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Message | null>(null);
  const [notice, setNotice] = useState<Message | null>(null);
  const request = useRef<AbortController | null>(null);
  const dirty = email !== "" || password !== "" || newPassword !== "";
  useUnsavedDraft("administrator-account", dirty);
  const visibleAccounts = page?.items.filter(account => `${account.email} ${account.id}`.toLowerCase().includes(query.trim().toLowerCase())) ?? [];
  const ownID = state.kind === "authenticated" ? state.administrator.id : "";

  const run = useCallback((work: (signal: AbortSignal) => Promise<void>, success?: Message) => {
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

  if (!administrators) return <section><h2>{t("Administrators")}</h2><p role="alert">{t("Administrator account management is unavailable.")}</p></section>;

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
    if (account.active && !window.confirm(t(bilingual(`Deactivate ${account.email}? All their sessions will be revoked.`, `${account.email} 관리자를 비활성화할까요? 모든 세션이 폐기됩니다.`)))) return;
    run(async (signal) => {
      const updated = await administrators!.setActive(account.id, !account.active, signal);
      if (!signal.aborted) setPage((current) => current && { ...current, items: current.items.map((item) => item.id === updated.id ? updated : item) });
    }, bilingual(`${account.email} is now ${account.active ? "inactive" : "active"}.`, `${account.email} 관리자를 ${account.active ? "비활성화" : "활성화"}했습니다.`));
  }

  function revoke(account: AdministratorProfile) {
    if (request.current || !window.confirm(t(bilingual(`Revoke all sessions for ${account.email}?${account.id === ownID ? " You will be signed out immediately." : ""}`, `${account.email} 관리자의 모든 세션을 폐기할까요?${account.id === ownID ? " 즉시 로그아웃됩니다." : ""}`)))) return;
    run((signal) => administrators!.revokeSessions(account.id, signal), bilingual(`All sessions revoked for ${account.email}.`, `${account.email} 관리자의 모든 세션을 폐기했습니다.`));
  }

  function editPassword(account: AdministratorProfile | null) {
    if (target?.id === account?.id && account !== null) return;
    if (newPassword !== "" && !window.confirm(t("Discard the unsaved administrator password?"))) return;
    setTarget(account); setNewPassword("");
  }

  function reset() {
    if (!target || request.current) return;
    if (target.id === ownID && !window.confirm(t("Replace your password and revoke all your sessions? You will be signed out immediately."))) return;
    const supplied = newPassword; setNewPassword("");
    run(async (signal) => {
      await administrators!.resetPassword(target.id, supplied, signal);
      if (!signal.aborted) setTarget(null);
    }, bilingual(`Password replaced for ${target.email}. All their sessions were revoked.`, `${target.email} 관리자의 비밀번호를 교체하고 모든 세션을 폐기했습니다.`));
  }

  return <section className="access-page administrators-page">
    <PageHeading title={t("Administrators")} description={t("Administrators have full access to this application. Your own account cannot be deactivated here.")} actions={<Button disabled={busy || dirty} onClick={() => load()} variant="secondary">{t("Refresh")}</Button>} />
    {error && <p className="access-feedback form-error" role="alert">{t(error)}</p>}
    {notice && <p className="access-feedback" role="status">{t(notice)}</p>}
    <form aria-label={t("Create administrator")} onSubmit={(event) => { event.preventDefault(); create(); }}>
      <FormSection disabled={busy} title={t("Create administrator")}>
        <div className="access-form-grid">
          <label className="access-field">{t("Email")}<input type="email" required autoComplete="off" placeholder="name@example.com" value={email} onChange={(event) => setEmail(event.target.value)} /></label>
          <div className="access-field"><label htmlFor="administrator-password">{t("Password")}</label><PasswordInput disabled={busy} id="administrator-password" aria-describedby="administrator-password-help" required autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} /></div>
        </div>
        <p className="access-help" id="administrator-password-help">{t("Use 12–1024 UTF-8 bytes. This sets a password directly; no invitation email is sent.")}</p>
        <div className="access-form-actions"><Button type="submit">{t("Create administrator")}</Button></div>
      </FormSection>
    </form>
    {target && <form aria-label={t(bilingual(`Set password for ${target.email}`, `${target.email} 비밀번호 설정`))} onSubmit={(event) => { event.preventDefault(); reset(); }}>
      <FormSection disabled={busy} title={t(bilingual(`Set password for ${target.email}`, `${target.email} 비밀번호 설정`))}>
        <p className="access-help" id="administrator-reset-help">{t("The replacement revokes every session for this administrator. No reset email is sent.")}{target.id === ownID && t(" You will be signed out immediately.")}</p>
        <div className="access-field"><label htmlFor="administrator-new-password">{t("New password")}</label><PasswordInput disabled={busy} id="administrator-new-password" aria-describedby="administrator-reset-help" required autoComplete="new-password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} /></div>
        <div className="access-form-actions"><Button onClick={() => editPassword(null)} variant="secondary">{t("Cancel")}</Button><Button type="submit">{t("Save password")}</Button></div>
      </FormSection>
    </form>}
    {!page && busy && <p role="status">{t("Loading administrator accounts…")}</p>}
    {page && <>
      <div className="access-search-toolbar"><label>{t("Search administrators")}<input type="search" placeholder={t("Email or account ID…")} value={query} onChange={event => setQuery(event.target.value)} /></label><span className="settings-help">{t(bilingual(`${visibleAccounts.length} of ${page.items.length} on this page`, `이 페이지 ${page.items.length}명 중 ${visibleAccounts.length}명`))}</span></div>
      <div className="access-list-heading"><h3>{t("Administrator accounts")}</h3><p>{t("Manage account status, passwords, and active sessions.")}</p></div>
      <div className="table-scroll" role="region" aria-label={t("Administrator accounts")} tabIndex={0}><table className="users-table">
        <thead><tr><th>{t("Email")}</th><th>{t("Status")}</th><th>{t("Created")}</th><th>{t("Actions")}</th></tr></thead>
        <tbody>{visibleAccounts.map((account) => <tr key={account.id}>
          <td>{account.email}{account.id === ownID && t(" (you)")}</td><td><span className="status-badge" data-status={account.active ? "active" : "inactive"}>{account.active ? t("Active") : t("Inactive")}</span></td><td><time dateTime={account.createdAt}>{formatDate(account.createdAt)}</time></td>
          <td><div className="access-item-actions"><Button disabled={busy || account.id === ownID} onClick={() => changeActive(account)} variant="secondary">{account.active ? t("Deactivate") : t("Activate")}</Button><Button disabled={busy} onClick={() => editPassword(account)} variant="secondary">{t("Set password")}</Button><Button disabled={busy} onClick={() => revoke(account)} variant="danger">{t("Revoke sessions")}</Button></div></td>
        </tr>)}</tbody>
      </table></div>
      {page.items.length > 0 && visibleAccounts.length === 0 && <p role="status">{t(bilingual(`No administrators match “${query}” on this page.`, `이 페이지에 “${query}”와 일치하는 관리자가 없습니다.`))}</p>}
      {page.items.length === 0 && <p>{t("No administrators on this page.")}</p>}
      <nav aria-label={t("Administrator pagination")} className="access-pagination"><Button disabled={busy || dirty} onClick={() => load()} variant="secondary">{t("First page")}</Button><Button disabled={busy || dirty || !page.nextAfter} onClick={() => load(page.nextAfter)} variant="secondary">{t("Next page")}</Button></nav>
    </>}
  </section>;
}
