import { t, message as bilingual, formatDate, useLocale } from "../i18n";
import { useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { ServiceToken } from "../backend/ports";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import { Button, FormSection, PageHeading } from "../components/ui";

/** Lists token metadata while deliberately keeping newly issued secrets only in page state. */
export function ServiceTokensPage() {
  useLocale();
  const { serviceTokens } = useBackend();
  const [items, setItems] = useState<ServiceToken[]>([]);
  const [name, setName] = useState("");
  const [query, setQuery] = useState("");
  const [ttlDays, setTTLDays] = useState("30");
  const [scopes, setScopes] = useState("");
  const [issuedSecret, setIssuedSecret] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const live = useRef(true);
  const mutation = useRef(false);
  useUnsavedDraft("service-token", name !== "" || scopes !== "" || ttlDays !== "30");

  function load(): void {
    setPending(true); setError(null);
    void serviceTokens.listServiceTokens().then((result) => {
      if (live.current) setItems(result);
    }).catch(() => {
      if (live.current) setError("Unable to load service tokens. Try again.");
    }).finally(() => { if (live.current) setPending(false); });
  }
  useEffect(() => { live.current = true; load(); return () => { live.current = false; }; }, [serviceTokens]);

  function issue(): void {
    if (pending || mutation.current) return;
    const days = Number(ttlDays);
    const ttlSeconds = days * 24 * 60 * 60;
    if (name.trim() === "" || !Number.isInteger(days) || days < 1 || ttlSeconds > 365 * 24 * 60 * 60) {
      setError("Enter a name and a lifetime from one day to 365 days."); return;
    }
    setError(null); setIssuedSecret(null);
    const parsedScopes = scopes.split(/\s|,/).filter(Boolean).map((scope) => {
      const separator = scope.lastIndexOf(":");
      return { collectionId: scope.slice(0, separator), action: scope.slice(separator + 1) };
    });
    if (parsedScopes.length < 1 || parsedScopes.length > 256 || parsedScopes.some((scope) => !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(scope.collectionId) || !["read", "create", "update", "delete"].includes(scope.action))) { setError("Scopes use collection UUID:read, :create, :update, or :delete."); return; }
    mutation.current = true; setPending(true);
    void serviceTokens.issueServiceToken({ name: name.trim(), ttlSeconds, scopes: parsedScopes as Array<{ collectionId: string; action: "read" | "create" | "update" | "delete" }> }, undefined).then((issued) => {
      if (!live.current) return;
      const { token, ...metadata } = issued;
      setItems((current) => [metadata, ...current]); setIssuedSecret(token); setName(""); setScopes(""); setTTLDays("30");
    }).catch(() => { if (live.current) setError("Unable to issue service token. Try again."); }).finally(() => { mutation.current = false; if (live.current) setPending(false); });
  }
  function revoke(item: ServiceToken): void {
    if (pending || mutation.current || !item.active) return;
    mutation.current = true; setPending(true); setError(null);
    void serviceTokens.revokeServiceToken(item.id).then(() => {
      if (live.current) setItems((current) => current.map((entry) => entry.id === item.id ? { ...entry, active: false } : entry));
    }).catch(() => { if (live.current) setError("Unable to revoke service token. Try again."); }).finally(() => { mutation.current = false; if (live.current) setPending(false); });
  }
  return <section className="access-page service-tokens-page">
    <PageHeading title={t("Service tokens")} description={t("Use service tokens for server-to-server access. The secret is shown once and is never stored in this page.")} actions={<Button disabled={pending} onClick={load} variant="secondary">{t("Reload")}</Button>} />
    {error && <p className="access-feedback form-error" role="alert">{t(error)}</p>}
    {issuedSecret && <section aria-label={t("New service token")} className="access-secret"><h3>{t("Copy this token now")}</h3><code>{issuedSecret}</code><Button onClick={() => setIssuedSecret(null)} variant="secondary">{t("I copied the token")}</Button></section>}
    <FormSection disabled={pending} title={t("Issue service token")}>
      <div className="access-form-grid">
        <label className="access-field">{t("Name")}<input placeholder={t("e.g. nightly-export")} value={name} onChange={(event) => setName(event.target.value)} /></label>
        <label className="access-field">{t("Lifetime (days)")}<input type="number" min={1} max={365} step={1} value={ttlDays} onChange={(event) => setTTLDays(event.target.value)} /></label>
      </div>
      <label className="access-field">{t("Scopes (comma separated UUID:action)")}<input aria-describedby="token-scopes-help" autoCapitalize="none" autoComplete="off" className="access-code-input" spellCheck={false} value={scopes} onChange={(event) => setScopes(event.target.value)} /></label>
      <p className="access-help" id="token-scopes-help">{t("Use a collection UUID followed by :read, :create, :update, or :delete. Separate scopes with commas.")}</p>
      <div className="access-form-actions"><Button onClick={issue}>{t("Issue token")}</Button></div>
    </FormSection>
    <div className="access-list-heading"><h3>{t("Issued tokens")}</h3><p>{t("Review expiry and collection access, or revoke a token.")}</p></div>
    <div className="access-search-toolbar"><label>{t("Search tokens")}<input type="search" placeholder={t("Token name or collection scope…")} value={query} onChange={event => setQuery(event.target.value)} /></label><span className="settings-help">{t(bilingual(`${items.length} tokens`, `${items.length}개 토큰`))}</span></div>
    <ul aria-label={t("Service tokens")} className="access-item-list">{items.map((item) => <li key={item.id} hidden={!`${item.name} ${item.scopes.map(scope => `${scope.collectionId}:${scope.action}`).join(" ")}`.toLowerCase().includes(query.trim().toLowerCase())}>
      <div className="access-item-heading"><div><strong>{item.name}</strong><p className="access-help">{t("Expires")}<time dateTime={item.expiresAt}>{formatDate(item.expiresAt)}</time></p></div><span className="status-badge" data-status={item.active ? "active" : "inactive"}>{item.active ? t("Active") : t("Revoked")}</span></div>
      <p className="access-code">{item.scopes.map((scope) => `${scope.collectionId}:${scope.action}`).join(", ") || t("No scopes")}</p>
      <div className="access-item-actions"><Button disabled={pending || !item.active} onClick={() => revoke(item)} variant="danger">{t("Revoke")}</Button></div>
    </li>)}</ul>
    {!pending && items.length === 0 && !error && <p className="access-empty">{t("No service tokens yet. Issue a token above when you need server-to-server access.")}</p>}
  </section>;
}
