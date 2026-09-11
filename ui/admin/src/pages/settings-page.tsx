import { useEffect, useState } from "react";
import type { AdminSettings } from "../backend/ports";
import { useBackend } from "../auth/session";
import { APIRateLimitSettings, ruleErrors } from "./api-rate-limit-settings";
import { ProfileSettings } from "./profile-settings";

export function SettingsPage() {
  const { settings: port } = useBackend();
  const [value, setValue] = useState<AdminSettings | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const c = new AbortController();
    if (port)
      void port
        .get(c.signal)
        .then(setValue)
        .catch(() => setMessage("Unable to load application settings."));
    return () => c.abort();
  }, [port]);
  if (!port)
    return (
      <section>
        <h2>Application settings</h2>
        <p>Settings are unavailable on this server.</p>
      </section>
    );
  if (!value)
    return (
      <section>
        <h2>Application settings</h2>
        <p>{message || "Loading settings…"}</p>
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
    const error = ruleErrors(
      settingsValue.rateLimits.rules ?? [],
      settingsValue.rateLimits.excludedIPs ?? [],
    );
    if (error) {
      setMessage(error);
      return;
    }
    setBusy(true);
    try {
      const saved = await settingsPort.save(settingsValue);
      setValue(saved);
      document.documentElement.dataset.theme = saved.application.theme;
      document.documentElement.style.setProperty(
        "--accent",
        saved.application.accent,
      );
      setMessage("Settings saved.");
    } catch {
      setMessage(
        "Settings changed on the server or were rejected. Reload before retrying.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h2>Application settings</h2>
      <p>Changes are validated and saved as one revision.</p>
      {message && <p role="status">{message}</p>}
      <ProfileSettings />
      <fieldset disabled={busy}>
        <legend>Branding and appearance</legend>
        <label>
          Theme
          <select
            value={value.application.theme}
            onChange={(e) => change("application", { theme: e.target.value })}
          >
            <option value="system">System</option>
            <option value="light">Light</option>
            <option value="dark">Dark</option>
          </select>
        </label>
        <label>
          Accent color
          <input
            type="color"
            value={value.application.accent}
            onChange={(e) => change("application", { accent: e.target.value })}
          />
        </label>
        <label>
          Branding
          <input
            value={value.application.branding}
            onChange={(e) =>
              change("application", { branding: e.target.value })
            }
          />
        </label>
      </fieldset>
      <fieldset disabled={busy}>
        <legend>Request logs</legend>
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
      </fieldset>
      <fieldset disabled={busy}>
        <legend>Administrator rate limits</legend>
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
      </fieldset>
      <APIRateLimitSettings
        disabled={busy}
        value={value}
        change={(patch) => change("rateLimits", patch)}
      />
      <fieldset disabled={busy}>
        <legend>Automatic backups</legend>
        <label>
          <input
            checked={value.backups.enabled}
            type="checkbox"
            onChange={(e) => change("backups", { enabled: e.target.checked })}
          />{" "}
          Enabled
        </label>
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
      </fieldset>
      <fieldset disabled={busy}>
        <legend>Trusted proxies</legend>
        <label>
          CIDRs, one per line
          <textarea
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
      </fieldset>
      <button disabled={busy} onClick={() => void save()} type="button">
        {busy ? "Saving…" : "Save settings"}
      </button>
    </section>
  );
}
