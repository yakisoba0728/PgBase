import { useCallback, useEffect, useState } from "react";
import { useBackend } from "../auth/session";
import type { AuthTokenPolicy } from "../backend/ports";
import { Button, FormSection } from "./ui";
import { TypedConfirmation, useFeatureResource, useMutationScope } from "./feature-state";
import { useUnsavedDraft } from "../routing/unsaved-draft";
const purposes = ["session", "verify", "password_reset", "email_change", "otp", "impersonation"];
const labels: Record<string, string> = { session: "Session", verify: "Email verification", password_reset: "Password reset", email_change: "Email change", otp: "One-time password", impersonation: "Impersonation" };
export function AuthTokenPolicies({ collectionId }: { collectionId: string }) {
  const { authCollections: port, capabilities } = useBackend();
  const [opened, setOpened] = useState(false);
  if (!port.tokenPolicies || !port.putTokenPolicy || capabilities?.authAdministration === false) return null;
  return <details className="auth-administration" onToggle={e => e.currentTarget.open && setOpened(true)}><summary>Token policies</summary>{opened && <PolicyList key={collectionId} collectionId={collectionId} />}</details>;
}
function PolicyList({ collectionId }: { collectionId: string }) {
  const { authCollections: port } = useBackend();
  const read = useCallback((signal: AbortSignal) => port.tokenPolicies!(collectionId, signal), [port, collectionId]);
  const resource = useFeatureResource(read);
  const [dirtyPolicies, setDirtyPolicies] = useState<Set<string>>(() => new Set());
  const onDirty = useCallback((purpose: string, dirty: boolean) => setDirtyPolicies(current => { if (current.has(purpose) === dirty) return current; const next = new Set(current); if (dirty) next.add(purpose); else next.delete(purpose); return next; }), []);
  const policies = resource.data;
  const [notice, setNotice] = useState("");
  return <div>{notice && <p role="status">{notice}</p>}{resource.error && <p role="alert">{resource.error}</p>}<Button variant="secondary" disabled={resource.loading || resource.busy || dirtyPolicies.size > 0} onClick={resource.reload}>Reload token policies</Button>{resource.loading && <p role="status">Loading token policies…</p>}{!resource.loading && resource.data && purposes.map(purpose => <PolicyEditor key={`${purpose}:${policies?.find(v => v.purpose === purpose)?.keyVersion ?? "default"}:${policies?.find(v => v.purpose === purpose)?.ttlSeconds ?? "default"}`} collectionId={collectionId} purpose={purpose} onDirty={onDirty} value={policies?.find(v => v.purpose === purpose)} onSaved={saved => { setNotice(`${labels[purpose]} token policy saved.`); resource.setData(current => [...(current ?? []).filter(v => v.purpose !== purpose), saved]); }} />)}</div>;
}
function PolicyEditor({ collectionId, purpose, value, onSaved, onDirty }: { onDirty(purpose: string, dirty: boolean): void; collectionId: string; purpose: string; value?: AuthTokenPolicy; onSaved(value: AuthTokenPolicy): void }) {
  const { authCollections: port } = useBackend();
  const [ttl, setTTL] = useState(value ? String(value.ttlSeconds) : "");
  const [rotate, setRotate] = useState(false);
  const [keyVersion, setKeyVersion] = useState("");
  const [key, setKey] = useState("");
  const [confirm, setConfirm] = useState(false);
  const mutation = useMutationScope();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const dirty = ttl !== (value ? String(value.ttlSeconds) : "") || key !== "" || keyVersion !== "";
  useEffect(() => { onDirty(purpose, dirty); return () => onDirty(purpose, false); }, [purpose, dirty, onDirty]);
  const release = useUnsavedDraft(`token-policy:${collectionId}:${purpose}`, ttl !== (value ? String(value.ttlSeconds) : "") || key !== "" || keyVersion !== "");
  const valid = /^\d+$/.test(ttl) && Number(ttl) >= 60 && Number(ttl) <= 86400;
  async function save() {
    if (busy || !valid) return;
    if (rotate && (!/^[A-Za-z0-9_-]{1,32}$/.test(keyVersion) || new TextEncoder().encode(key).length < 32 || new TextEncoder().encode(key).length > 4096)) { setMessage("Use a key version of 1–32 letters, digits, underscores or hyphens, and a key of 32–4,096 UTF-8 bytes."); return; }
    const signal = mutation.begin(); if (!signal) return;
    setBusy(true); setMessage("");
    try { const saved = await port.putTokenPolicy!(collectionId, purpose, { ttlSeconds: Number(ttl), ...(rotate ? { keyVersion, key } : {}) }, signal); if (!mutation.current(signal)) return; setKey(""); setKeyVersion(""); setConfirm(false); release(); onSaved(saved); setMessage("Token policy saved."); }
    catch { if (mutation.current(signal)) setMessage("The server could not confirm this policy change. Reload policies before retrying."); }
    finally { if (mutation.current(signal)) setBusy(false); mutation.end(signal); }
  }
  return <FormSection title={labels[purpose]} disabled={busy}>
    <p className="settings-help">{value ? `Key ${value.keyConfigured ? "configured" : "not configured"} · version ${value.keyVersion || "not set"}` : "Uses the server default until you save a policy."}</p>
    <label>Token lifetime in seconds<input type="number" min={60} max={86400} step={1} value={ttl} onChange={e => setTTL(e.target.value)} /><span className="settings-help">60–86,400 seconds. Changing the duration keeps the current signing key.</span></label>
    <label><input type="checkbox" checked={rotate} onChange={e => { setRotate(e.target.checked); setConfirm(false); if (!e.target.checked) { setKey(""); setKeyVersion(""); } }} />Replace signing key</label>
    {rotate && <><p role="note">Replacing the key invalidates tokens signed by the previous key.</p><div className="settings-field-grid"><label>New key version<input autoComplete="off" maxLength={32} value={keyVersion} onChange={e => setKeyVersion(e.target.value)} /></label><label>New signing key<input autoComplete="new-password" type="password" value={key} onChange={e => setKey(e.target.value)} /></label></div></>}
    {message && <p role="status">{message}</p>}
    {confirm ? <TypedConfirmation subject={purpose} action="Replace signing key" busy={busy} onConfirm={() => void save()} onCancel={() => setConfirm(false)} /> : <Button disabled={!valid || busy} onClick={() => rotate ? setConfirm(true) : void save()}>Save token policy</Button>}
  </FormSection>;
}
