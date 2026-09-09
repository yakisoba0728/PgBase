import { useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { AuthAdministration, AuthMailTemplate, AuthPolicy, AuthSMTPSettings, OAuthProviderSettings } from "../backend/ports";
import { providerPresets } from "../oauth-provider-presets";

const purposes: AuthMailTemplate["purpose"][] = ["verify", "password_reset", "email_change", "otp"];
const emptyProvider = (): OAuthProviderSettings => ({ name: "", enabled: true, authorizationUrl: "", tokenUrl: "", userInfoUrl: "", clientId: "", clientSecret: "", secretConfigured: false, scopes: [], redirectUris: [], subjectField: "sub", emailField: "email", emailVerifiedField: "email_verified", avatarField: "picture", allowInsecureLoopback: false });
const emptySMTP = (): AuthSMTPSettings => ({ address: "", from: "", username: "", password: "", passwordConfigured: false, serverName: "", implicitTls: false, allowInsecureLoopback: false });

export function AuthAdministrationPanel({ collectionId }: { collectionId: string }) {
  const { authCollections } = useBackend();
  const [value, setValue] = useState<AuthAdministration | null>(null);
  const [provider, setProvider] = useState(emptyProvider);
  const [smtp, setSMTP] = useState(emptySMTP);
  const [recipient, setRecipient] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [reloads, setReloads] = useState(0);
  const mutation = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; mutation.current?.abort(); }; }, []);
  useEffect(() => {
    const c = new AbortController(); setValue(null); setMessage(null); setProvider(emptyProvider());
    const timeout = window.setTimeout(() => { c.abort(); setMessage("Authentication settings timed out. Retry to reload."); }, 10_000);
    void authCollections.getAdministration(collectionId, c.signal).then((next) => {
      if (!c.signal.aborted) { setValue({ ...next, providers: next.providers ?? [], templates: next.templates ?? [] }); setSMTP(next.smtp ?? emptySMTP()); }
    }).catch(() => { if (!c.signal.aborted) setMessage("Unable to load authentication settings."); }).finally(() => window.clearTimeout(timeout));
    return () => { c.abort(); mutation.current?.abort(); window.clearTimeout(timeout); };
  }, [authCollections, collectionId, reloads]);
  function run(work: (signal: AbortSignal) => Promise<void>, success: string) {
    if (mutation.current) return;
    const c = new AbortController(); mutation.current = c; setBusy(true); setMessage(null);
    const timeout = window.setTimeout(() => { c.abort(); if (mounted.current) { setMessage("Unable to confirm the change. Reload settings before retrying."); setBusy(false); mutation.current = null; } }, 10_000);
    void work(c.signal).then(() => { if (!c.signal.aborted) setMessage(success); }).catch(() => { if (!c.signal.aborted) setMessage("Unable to save authentication settings."); }).finally(() => { window.clearTimeout(timeout); if (mutation.current === c) { mutation.current = null; if (mounted.current) setBusy(false); } });
  }
  function savePolicy(policy: AuthPolicy) { run(async (signal) => { const saved = await authCollections.setPolicy(collectionId, policy, signal); if (signal.aborted) return; setValue((current) => current && ({ ...current, policy: saved })); }, "Policy saved. Existing sessions are revoked when password authentication is disabled or password requirements tighten."); }
  function saveProvider() { run(async (signal) => { const saved = await authCollections.putProvider(collectionId, provider, signal); if (signal.aborted) return; setValue((current) => current && ({ ...current, providers: [...current.providers.filter((item) => item.name !== saved.name), saved] })); setProvider(emptyProvider()); }, "OAuth provider saved."); }
  function removeProvider(name: string) { run(async (signal) => { await authCollections.deleteProvider(collectionId, name, signal); if (signal.aborted) return; setValue((current) => current && ({ ...current, providers: current.providers.filter((item) => item.name !== name) })); }, "OAuth provider removed."); }
  function saveTemplate(template: AuthMailTemplate) { run(async (signal) => { const saved = await authCollections.putTemplate(collectionId, template, signal); if (signal.aborted) return; setValue((current) => current && ({ ...current, templates: [...current.templates.filter((item) => item.purpose !== saved.purpose), saved] })); }, "Mail template saved."); }
  function saveSMTP() { run(async (signal) => { const saved = await authCollections.putSMTP(collectionId, smtp, signal); if (signal.aborted) return; setSMTP(saved); setValue((current) => current && ({ ...current, smtp: saved })); }, "SMTP settings saved. Stored passwords are never returned."); }
  if (!value) return <div><p>{message ?? "Loading authentication settings…"}</p>{message && <button type="button" onClick={() => setReloads(v => v + 1)}>Retry authentication settings</button>}</div>;
  return <details className="auth-administration"><summary>Authentication settings</summary>
    {message && <p role="status">{message}</p>}
    <button type="button" disabled={busy} onClick={() => setReloads(v => v + 1)}>Reload authentication settings</button>
    <fieldset disabled={busy}><legend>Password and MFA policy</legend>
      <label><input type="checkbox" checked={value.policy.passwordEnabled} onChange={(e) => setValue({ ...value, policy: { ...value.policy, passwordEnabled: e.target.checked } })} />Password authentication</label>
      <label>Minimum password bytes <input type="number" min={12} max={128} value={value.policy.minimumPasswordBytes} onChange={(e) => setValue({ ...value, policy: { ...value.policy, minimumPasswordBytes: Number(e.target.value) } })} /></label>
      <label><input type="checkbox" checked={value.policy.mfaEnabled} onChange={(e) => setValue({ ...value, policy: { ...value.policy, mfaEnabled: e.target.checked } })} />TOTP and recovery codes</label>
      <p>Disabling TOTP deletes enrolled factors and recovery codes in this realm. Re-enabling requires users to enroll again.</p>
      <label><input type="checkbox" checked={value.policy.otpEnabled} onChange={(e) => setValue({ ...value, policy: { ...value.policy, otpEnabled: e.target.checked } })} />Email one-time-code login</label>
      <label>OTP lifetime in seconds <input type="number" min={60} max={900} value={value.policy.otpDurationSeconds} onChange={(e) => setValue({ ...value, policy: { ...value.policy, otpDurationSeconds: Number(e.target.value) } })} /></label>
      <label>OTP attempts <input type="number" min={1} max={10} value={value.policy.otpMaxAttempts} onChange={(e) => setValue({ ...value, policy: { ...value.policy, otpMaxAttempts: Number(e.target.value) } })} /></label>
      <button type="button" onClick={() => savePolicy(value.policy)}>Save policy</button>
    </fieldset>
    <fieldset disabled={busy}><legend>OAuth providers</legend>
      {value.providers.map((item) => <div key={item.name}><strong>{item.name}</strong> ({item.enabled ? "enabled" : "disabled"}) <button type="button" onClick={() => setProvider({ ...item, clientSecret: "" })}>Edit</button> <button type="button" onClick={() => removeProvider(item.name)}>Remove</button></div>)}
      <label>Provider preset <select value="" onChange={(e) => { const preset = providerPresets.find((item) => item.value.name === e.target.value); if (preset) setProvider({ ...emptyProvider(), ...preset.value }); }}><option value="">Custom OAuth 2.0 / OIDC</option>{providerPresets.map((item) => <option key={item.value.name} value={item.value.name}>{item.label}</option>)}</select></label>
      <label>Name <input value={provider.name} onChange={(e) => setProvider({ ...provider, name: e.target.value })} /></label>
      <label>Authorization URL <input value={provider.authorizationUrl} onChange={(e) => setProvider({ ...provider, authorizationUrl: e.target.value })} /></label>
      <label>Token URL <input value={provider.tokenUrl} onChange={(e) => setProvider({ ...provider, tokenUrl: e.target.value })} /></label>
      <label>User info URL <input value={provider.userInfoUrl} onChange={(e) => setProvider({ ...provider, userInfoUrl: e.target.value })} /></label>
      <label>Client ID <input value={provider.clientId} onChange={(e) => setProvider({ ...provider, clientId: e.target.value })} /></label>
      <label>Client secret <input type="password" placeholder={provider.secretConfigured ? "Stored; leave blank to keep" : ""} value={provider.clientSecret ?? ""} onChange={(e) => setProvider({ ...provider, clientSecret: e.target.value })} /></label>
      <label>Scopes (space separated) <input value={provider.scopes.join(" ")} onChange={(e) => setProvider({ ...provider, scopes: e.target.value.split(/\s+/).filter(Boolean) })} /></label>
      <label>Avatar URL field <input value={provider.avatarField} onChange={(e) => setProvider({ ...provider, avatarField: e.target.value })} /></label>
      <label>Redirect URIs (one per line) <textarea value={provider.redirectUris.join("\n")} onChange={(e) => setProvider({ ...provider, redirectUris: e.target.value.split("\n").map((v) => v.trim()).filter(Boolean) })} /></label>
      <label><input type="checkbox" checked={provider.enabled} onChange={(e) => setProvider({ ...provider, enabled: e.target.checked })} />Enabled</label>
      <label><input type="checkbox" checked={provider.allowInsecureLoopback} onChange={(e) => setProvider({ ...provider, allowInsecureLoopback: e.target.checked })} />Allow HTTP for loopback development endpoints</label>
      <button type="button" onClick={saveProvider}>Save provider</button>
    </fieldset>
    <fieldset disabled={busy}><legend>Mail templates</legend><p>Action templates use <code>{"{{action_url}}"}</code>. OTP templates use <code>{"{{otp}}"}</code> and may include <code>{"{{otp_id}}"}</code>.</p>
      {purposes.map((purpose) => { const current = value.templates.find((t) => t.purpose === purpose) ?? (purpose === "otp" ? { purpose, subject: "Your login code", body: "Your one-time code is {{otp}}", htmlBody: "<p>Your one-time code is <strong>{{otp}}</strong></p>" } : { purpose, subject: "", body: "{{action_url}}", htmlBody: "<p><a class=\"button\" href=\"{{action_url}}\">Continue</a></p>" }); return <TemplateEditor key={`${purpose}:${current.subject}:${current.body}`} value={current} save={saveTemplate} preview={(draft, signal) => authCollections.previewTemplate(collectionId, { ...draft, actionUrl: "https://app.example/auth/action" }, signal)} />; })}
    </fieldset>
    <fieldset disabled={busy}><legend>SMTP</legend>
      <label>Server address <input placeholder="localhost:1025" value={smtp.address} onChange={(e) => setSMTP({ ...smtp, address: e.target.value })} /></label><label>From <input value={smtp.from} onChange={(e) => setSMTP({ ...smtp, from: e.target.value })} /></label><label>Username <input value={smtp.username} onChange={(e) => setSMTP({ ...smtp, username: e.target.value })} /></label><label>Password <input type="password" placeholder={smtp.passwordConfigured ? "Stored; leave blank to keep" : ""} value={smtp.password ?? ""} onChange={(e) => setSMTP({ ...smtp, password: e.target.value })} /></label><label>Server name <input value={smtp.serverName} onChange={(e) => setSMTP({ ...smtp, serverName: e.target.value })} /></label>
      <label><input type="checkbox" checked={smtp.implicitTls} onChange={(e) => setSMTP({ ...smtp, implicitTls: e.target.checked })} />Implicit TLS</label><label><input type="checkbox" checked={smtp.allowInsecureLoopback} onChange={(e) => setSMTP({ ...smtp, allowInsecureLoopback: e.target.checked })} />Allow insecure local SMTP</label><button type="button" onClick={saveSMTP}>Save SMTP</button>
      <label>Local test recipient <input value={recipient} onChange={(e) => setRecipient(e.target.value)} /></label><button type="button" onClick={() => run((signal) => authCollections.testSMTP(collectionId, recipient, signal), `Test message sent to ${recipient}.`)}>Send test</button>
    </fieldset>
  </details>;
}

function TemplateEditor({ value, save, preview }: { value: AuthMailTemplate; save: (value: AuthMailTemplate) => void; preview: (value: AuthMailTemplate, signal: AbortSignal) => Promise<{ subject: string; text: string; html: string }> }) { const [draft, setDraft] = useState(value); const [rendered, setRendered] = useState<{ subject: string; text: string; html: string } | null>(null); const [previewError, setPreviewError] = useState(""); function runPreview() { const c = new AbortController(); setPreviewError(""); void preview(draft, c.signal).then(setRendered).catch(() => setPreviewError("Template is invalid or could not be previewed.")); } return <div><h4>{value.purpose.replace("_", " ")}</h4><label>Subject <input value={draft.subject} onChange={(e) => setDraft({ ...draft, subject: e.target.value })} /></label><label>Plain text alternative <textarea value={draft.body} onChange={(e) => setDraft({ ...draft, body: e.target.value })} /></label><label>HTML body <textarea value={draft.htmlBody} onChange={(e) => setDraft({ ...draft, htmlBody: e.target.value })} /></label><button type="button" onClick={runPreview}>Validate and preview</button>{previewError && <p role="alert">{previewError}</p>}{rendered && <details open><summary>{rendered.subject}</summary><pre>{rendered.text}</pre>{rendered.html && <iframe title={`${draft.purpose} email preview`} sandbox="" srcDoc={rendered.html} />}</details>}<button type="button" onClick={() => save(draft)}>Save template</button></div>; }
