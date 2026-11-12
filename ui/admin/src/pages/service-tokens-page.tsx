import { useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { ServiceToken } from "../backend/ports";
import { Button, FormSection, PageHeading } from "../components/ui";

const defaultTTLSeconds = 30 * 24 * 60 * 60;

/** Lists token metadata while deliberately keeping newly issued secrets only in page state. */
export function ServiceTokensPage() {
  const { serviceTokens } = useBackend();
  const [items, setItems] = useState<ServiceToken[]>([]);
  const [name, setName] = useState("");
  const [ttlDays, setTTLDays] = useState("30");
  const [scopes, setScopes] = useState("");
  const [issuedSecret, setIssuedSecret] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const live = useRef(true);

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
    const ttlSeconds = Math.round(Number(ttlDays) * 24 * 60 * 60);
    if (name.trim() === "" || !Number.isInteger(ttlSeconds) || ttlSeconds < 1 || ttlSeconds > 365 * 24 * 60 * 60) {
      setError("Enter a name and a lifetime from one day to 365 days."); return;
    }
    setError(null); setIssuedSecret(null);
    const parsedScopes = scopes.split(/\s|,/).filter(Boolean).map((scope) => {
      const separator = scope.lastIndexOf(":");
      return { collectionId: scope.slice(0, separator), action: scope.slice(separator + 1) };
    });
    if (parsedScopes.length < 1 || parsedScopes.length > 256 || parsedScopes.some((scope) => !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(scope.collectionId) || !["read", "create", "update", "delete"].includes(scope.action))) { setError("Scopes use collection UUID:read, :create, :update, or :delete."); return; }
    setPending(true);
    void serviceTokens.issueServiceToken({ name: name.trim(), ttlSeconds, scopes: parsedScopes as Array<{ collectionId: string; action: "read" | "create" | "update" | "delete" }> }, undefined).then((issued) => {
      if (!live.current) return;
      const { token, ...metadata } = issued;
      setItems((current) => [metadata, ...current]); setIssuedSecret(token); setName(""); setScopes(""); setTTLDays("30");
    }).catch(() => { if (live.current) setError("Unable to issue service token. Try again."); }).finally(() => { if (live.current) setPending(false); });
  }
  function revoke(item: ServiceToken): void {
    if (pending || !item.active) return;
    setPending(true); setError(null);
    void serviceTokens.revokeServiceToken(item.id).then(() => {
      if (live.current) setItems((current) => current.map((entry) => entry.id === item.id ? { ...entry, active: false } : entry));
    }).catch(() => { if (live.current) setError("Unable to revoke service token. Try again."); }).finally(() => { if (live.current) setPending(false); });
  }
  return <section className="access-page service-tokens-page">
    <PageHeading title="Service tokens" description="Use service tokens for server-to-server access. The secret is shown once and is never stored in this page." actions={<Button disabled={pending} onClick={load} variant="secondary">Reload</Button>} />
    {error && <p className="access-feedback form-error" role="alert">{error}</p>}
    {issuedSecret && <section aria-label="New service token" className="access-secret"><h3>Copy this token now</h3><code>{issuedSecret}</code><Button onClick={() => setIssuedSecret(null)} variant="secondary">I copied the token</Button></section>}
    <FormSection disabled={pending} title="Issue service token">
      <div className="access-form-grid">
        <label className="access-field">Name <input placeholder="e.g. nightly-export" value={name} onChange={(event) => setName(event.target.value)} /></label>
        <label className="access-field">Lifetime (days)<input type="number" value={ttlDays} onChange={(event) => setTTLDays(event.target.value)} /></label>
      </div>
      <label className="access-field">Scopes (comma separated UUID:action)<input aria-describedby="token-scopes-help" autoCapitalize="none" autoComplete="off" className="access-code-input" spellCheck={false} value={scopes} onChange={(event) => setScopes(event.target.value)} /></label>
      <p className="access-help" id="token-scopes-help">Use a collection UUID followed by :read, :create, :update, or :delete. Separate scopes with commas.</p>
      <div className="access-form-actions"><Button onClick={issue}>Issue token</Button></div>
    </FormSection>
    <div className="access-list-heading"><h3>Issued tokens</h3><p>Review expiry and collection access, or revoke a token.</p></div>
    <ul aria-label="Service tokens" className="access-item-list">{items.map((item) => <li key={item.id}>
      <div className="access-item-heading"><div><strong>{item.name}</strong><p className="access-help">Expires <time dateTime={item.expiresAt}>{new Date(item.expiresAt).toLocaleString()}</time></p></div><span className="access-status">{item.active ? "Active" : "Revoked"}</span></div>
      <p className="access-code">{item.scopes.map((scope) => `${scope.collectionId}:${scope.action}`).join(", ") || "No scopes"}</p>
      <div className="access-item-actions"><Button disabled={pending || !item.active} onClick={() => revoke(item)} variant="danger">Revoke</Button></div>
    </li>)}</ul>
    {!pending && items.length === 0 && !error && <p className="access-empty">No service tokens yet. Issue a token above when you need server-to-server access.</p>}
  </section>;
}
