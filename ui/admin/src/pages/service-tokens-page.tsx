import { useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { ServiceToken } from "../backend/ports";

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
  return <section><h2>Service tokens</h2><p>Use service tokens for server-to-server access. The secret is shown once and is never stored in this page.</p>
    {error && <p className="form-error" role="alert">{error}</p>}
    {issuedSecret && <section aria-label="New service token"><h3>Copy this token now</h3><code>{issuedSecret}</code><button type="button" onClick={() => setIssuedSecret(null)}>I copied the token</button></section>}
    <fieldset disabled={pending}><legend>Issue service token</legend><label>Name <input value={name} onChange={(event) => setName(event.target.value)} /></label><label>Lifetime (days)<input type="number" value={ttlDays} onChange={(event) => setTTLDays(event.target.value)} /></label><label>Scopes (comma separated UUID:action)<input value={scopes} onChange={(event) => setScopes(event.target.value)} /></label><button type="button" onClick={issue}>Issue token</button><button type="button" onClick={load}>Reload</button></fieldset>
    <ul aria-label="Service tokens">{items.map((item) => <li key={item.id}><strong>{item.name}</strong> · {item.active ? "Active" : "Revoked"} · expires {item.expiresAt}<p>{item.scopes.map((scope) => `${scope.collectionId}:${scope.action}`).join(", ") || "No scopes"}</p><button disabled={pending || !item.active} type="button" onClick={() => revoke(item)}>Revoke</button></li>)}</ul>
  </section>;
}
