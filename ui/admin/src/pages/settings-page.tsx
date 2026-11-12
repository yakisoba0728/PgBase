import { useMutationScope } from "../components/feature-state";
import { RuntimePolicySettings, runtimePolicyError } from "./runtime-policy-settings";
import { applyWorkspaceTheme } from "../components/workspace-theme";
import { useEffect, useRef, useState } from "react";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import type { AdminSettings } from "../backend/ports";
import { useBackend } from "../auth/session";
import { APIRateLimitSettings, ruleErrors } from "./api-rate-limit-settings";
import { ProfileSettings } from "./profile-settings";
import { Button, FormSection, PageHeading } from "../components/ui";

export function SettingsPage() {
  const { settings: port, profile: profilePort } = useBackend();
  const [value, setValue] = useState<AdminSettings | null>(null);
  const [keep, setKeep] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const mutation = useMutationScope();
  const savedValue = useRef<AdminSettings | null>(null);
  const dirty = value !== null && savedValue.current !== null &&
    (JSON.stringify(value) !== JSON.stringify(savedValue.current) || keep !== String(savedValue.current.backups.keep));
  useUnsavedDraft("application-settings", dirty);
  useEffect(() => {
    const c = new AbortController();
    if (port)
      void port
        .get(c.signal)
        .then((settings) => { savedValue.current = settings; setValue(settings); setKeep(String(settings.backups.keep)); })
        .catch(() => setMessage("Unable to load application settings."));
    return () => c.abort();
  }, [port]);
  if (!port)
    return (
      <section className="settings-page">
        <PageHeading title="Application settings" />
        <p>Settings are unavailable on this server.</p>
      </section>
    );
  if (!value)
    return (
      <section className="settings-page">
        <PageHeading title="Application settings" />
        <p role="status">{message || "Loading settings…"}</p>
      </section>
    );
  const settingsPort = port;
  const settingsValue = value;
  const change = (
    key: "application" | "logs" | "backups" | "proxy" | "rateLimits",
    patch: Record<string, unknown>,
  ) =>
    setValue(
      (current) =>
        current &&
        ({ ...current, [key]: { ...current[key], ...patch } } as AdminSettings),
    );
  const trustedProxyCIDRs = Array.isArray(value.proxy.trustedProxyCIDRs)
    ? value.proxy.trustedProxyCIDRs
    : [];
  async function save() {
    if (!/^\d+$/.test(keep) || Number(keep) > 10000) {
      setMessage("Backups to keep must be a whole number from 0 through 10000.");
      return;
    }
    const policyError = runtimePolicyError(settingsValue);
    if (policyError) { setMessage(policyError); return; }
    const error = ruleErrors(
      settingsValue.rateLimits.rules ?? [],
      settingsValue.rateLimits.excludedIPs ?? [],
    );
    if (error) {
      setMessage(error);
      return;
    }
    const signal = mutation.begin(); if (!signal) return;
    setBusy(true);
    try {
      const saved = await settingsPort.save({ ...settingsValue, backups: { ...settingsValue.backups, keep: Number(keep) } }, signal);
      if (!mutation.current(signal)) return;
      savedValue.current = saved;
      setValue(saved);
      setKeep(String(saved.backups.keep));
      applyWorkspaceTheme(saved.application);
      setMessage("Settings saved.");
    } catch {
      if (!mutation.current(signal)) return;
      setMessage(
        "Settings changed on the server or were rejected. Reload before retrying.",
      );
    } finally {
      if (mutation.current(signal)) setBusy(false); mutation.end(signal);
    }
  }
  async function reloadServer() {
    if (busy || dirty) return;
    const signal = mutation.begin(); if (!signal) return;
    setBusy(true); setMessage("");
    try { await settingsPort.reload(signal); if (!mutation.current(signal)) return; const fresh = await settingsPort.get(signal); if (!mutation.current(signal)) return; savedValue.current = fresh; setValue(fresh); setKeep(String(fresh.backups.keep)); applyWorkspaceTheme(fresh.application); setMessage("Server settings reloaded from storage."); }
    catch { if (mutation.current(signal)) setMessage("Server settings could not be reloaded. Your current draft is preserved."); }
    finally { if (mutation.current(signal)) setBusy(false); mutation.end(signal); }
  }
  return (
    <section className="settings-page">
      <PageHeading title="Application settings" description="Manage your administrator profile and application preferences." actions={<Button variant="secondary" disabled={busy || dirty} onClick={() => void reloadServer()}>Reload server settings</Button>} />
      <nav className="section-navigation" aria-label="Settings sections">
        {profilePort && <a href="#settings-profile">Profile</a>}
        <a href="#settings-appearance">Appearance</a>
        <a href="#settings-logs">Logs</a>
        <a href="#settings-security">Security</a><a href="#settings-batch">Batch requests</a>
        <a href="#settings-backups">Backups</a>
        <a href="#settings-network">Network</a>
      </nav>
      {message && <p className="settings-feedback" role="status">{message}</p>}
      <ProfileSettings />
      <FormSection id="settings-appearance" disabled={busy} title="Branding and appearance">
        <div className="settings-field-grid">
        <div className="settings-field">
        <label>
          Theme
          <select
            aria-describedby="settings-theme-help"
            value={value.application.theme}
            onChange={(e) => change("application", { theme: e.target.value })}
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </label>
        <p id="settings-theme-help" className="settings-help">System follows this computer’s light or dark appearance.</p>
        </div>
        <div className="settings-field">
        <label>
          Accent color
          <input
            type="color"
            aria-describedby="settings-accent-help"
            className={["", "#1d4ed8", "#ded6ee"].includes(value.application.accent.trim().toLowerCase()) ? "theme-accent" : ""}
            value={value.application.accent || "#ded6ee"}
            onChange={(e) => change("application", { accent: e.target.value })}
          />
        </label>
        <p id="settings-accent-help" className="settings-help">Choose a color to replace the theme accent.</p>
        </div>
        <label>
          Branding
          <input
            value={value.application.branding}
            onChange={(e) =>
              change("application", { branding: e.target.value })
            }
          />
        </label>
        </div>
      </FormSection>
      <FormSection id="settings-logs" disabled={busy} title="Request logs">
        <div className="settings-field-grid">
        <label>
          Retention days
          <input
            type="number"
            min="1"
            value={value.logs.retentionDays}
            onChange={(e) =>
              change("logs", { retentionDays: Number(e.target.value) })
            }
          />
        </label>
        <label>
          Level
          <select
            value={value.logs.level}
            onChange={(e) => change("logs", { level: e.target.value })}
          >
            {["debug", "info", "warn", "error"].map((level) => (
              <option key={level}>{level}</option>
            ))}
          </select>
        </label>
        </div>
        <div className="settings-checks">
        <label>
          <input
            checked={value.logs.includeIP}
            type="checkbox"
            onChange={(e) => change("logs", { includeIP: e.target.checked })}
          />{" "}
          Include IP addresses
        </label>
        <label>
          <input
            checked={value.logs.includeAuth}
            type="checkbox"
            onChange={(e) => change("logs", { includeAuth: e.target.checked })}
          />{" "}
          Include authentication events
        </label>
        </div>
      </FormSection>
      <div id="settings-security" className="settings-security">
      <FormSection disabled={busy} title="Administrator rate limits">
        <p className="settings-help">Limit repeated administrator sign-in failures by account and network.</p>
        <div className="settings-field-grid">
        <label>
          Account failures
          <input
            type="number"
            min="1"
            value={value.rateLimits.admin.accountMaxFailures}
            onChange={(e) =>
              change("rateLimits", {
                admin: {
                  ...value.rateLimits.admin,
                  accountMaxFailures: Number(e.target.value),
                },
              })
            }
          />
        </label>
        <label>
          Network failures
          <input
            type="number"
            min="1"
            value={value.rateLimits.admin.networkMaxFailures}
            onChange={(e) =>
              change("rateLimits", {
                admin: {
                  ...value.rateLimits.admin,
                  networkMaxFailures: Number(e.target.value),
                },
              })
            }
          />
        </label>
        <label>
          Window seconds
          <input
            type="number"
            min="1"
            value={value.rateLimits.admin.windowSeconds}
            onChange={(e) =>
              change("rateLimits", {
                admin: {
                  ...value.rateLimits.admin,
                  windowSeconds: Number(e.target.value),
                },
              })
            }
          />
        </label>
        <label>
          Block seconds
          <input
            type="number"
            min="1"
            value={value.rateLimits.admin.blockSeconds}
            onChange={(e) =>
              change("rateLimits", {
                admin: {
                  ...value.rateLimits.admin,
                  blockSeconds: Number(e.target.value),
                },
              })
            }
          />
        </label>
        </div>
      </FormSection>
      <RuntimePolicySettings value={value} disabled={busy} change={setValue} />
      <APIRateLimitSettings
        disabled={busy}
        value={value}
        change={(patch) => change("rateLimits", patch)}
      />
      </div>
      <FormSection id="settings-backups" disabled={busy} title="Automatic backups">
        <label className="settings-toggle">
          <input
            checked={value.backups.enabled}
            type="checkbox"
            onChange={(e) => change("backups", { enabled: e.target.checked })}
          />{" "}
          Enabled
        </label>
        <div className="settings-field-grid">
        <label>
          Backups to keep
          <input type="number" min="0" max="10000" step="1" value={keep} aria-describedby="settings-backup-keep-help" onChange={(event) => setKeep(event.target.value)} />
        </label>
        <p className="settings-help" id="settings-backup-keep-help">Counts all archives: manual, uploaded, and scheduled. Use 0 to keep every archive.</p>
        <label>
          Schedule
          <input
            value={value.backups.schedule}
            onChange={(e) => change("backups", { schedule: e.target.value })}
          />
        </label>
        <label>
          Timezone
          <input
            value={value.backups.timezone}
            onChange={(e) => change("backups", { timezone: e.target.value })}
          />
        </label>
        </div>
      </FormSection>
      <FormSection id="settings-network" disabled={busy} title="Trusted proxies">
        <p className="settings-help">Configure the proxy networks trusted by this application.</p>
        <label>
          CIDRs, one per line
          <textarea
            rows={3}
            spellCheck={false}
            autoCapitalize="none"
            className="settings-code"
            value={trustedProxyCIDRs.join("\n")}
            onChange={(e) =>
              change("proxy", {
                trustedProxyCIDRs: e.target.value
                  .split("\n")
                  .map((s) => s.trim())
                  .filter(Boolean),
              })
            }
          />
        </label>
      </FormSection>
      <div className="settings-save-bar">
        <p>{dirty ? "Unsaved application changes. " : "Application changes are validated and saved as one revision."}{dirty && "Save settings to apply them together."}</p>
      {dirty && <Button variant="secondary" disabled={busy} onClick={() => { if (savedValue.current) { setValue(savedValue.current); setKeep(String(savedValue.current.backups.keep)); setMessage("Unsaved application settings discarded."); } }}>Discard application changes</Button>}
      <Button disabled={busy} onClick={() => void save()}>
        {busy ? "Saving…" : "Save settings"}
      </Button>
      </div>
    </section>
  );
}
