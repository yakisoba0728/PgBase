import { t, useLocale } from "../i18n";
import { useState } from "react";
import type { AuthSMTPSettings } from "../backend/ports";

export function SMTPSettings({
  value,
  disabled,
  change,
  save,
  test,
}: {
  value: AuthSMTPSettings;
  disabled: boolean;
  change(value: AuthSMTPSettings): void;
  save(): void;
  test(recipient: string): void;
}) {
  useLocale();
  const [recipient, setRecipient] = useState("");
  const patch = (next: Partial<AuthSMTPSettings>) =>
    change({ ...value, ...next });
  return (
    <fieldset disabled={disabled} className="smtp-settings">
      <legend>SMTP</legend>
      <p className="settings-help">{t("Configure delivery for authentication emails. Save the connection before sending a test message.")}</p>
      <div className="settings-field-grid">
      <label>
        {t("Server address")}{" "}
        <input
          placeholder="localhost:1025"
          value={value.address}
          onChange={(e) => patch({ address: e.target.value })}
        />
      </label>
      <label>
        {t("From")}{" "}
        <input
          type="email" autoComplete="off" value={value.from}
          onChange={(e) => patch({ from: e.target.value })}
        />
      </label>
      <label>
        {t("Username")}{" "}
        <input
          value={value.username}
          onChange={(e) => patch({ username: e.target.value })}
        />
      </label>
      <label>
        {t("Password")}{" "}
        <input
          type="password" autoComplete="new-password"
          placeholder={
            value.passwordConfigured ? t("Stored; leave blank to keep") : ""
          }
          value={value.password ?? ""}
          onChange={(e) => patch({ password: e.target.value })}
        />
      </label>
      <label>
        {t("Server name")}{" "}
        <input
          value={value.serverName}
          onChange={(e) => patch({ serverName: e.target.value })}
        />
      </label>
      </div>
      <div className="settings-checks">
      <label>
        <input
          type="checkbox"
          checked={value.implicitTls}
          onChange={(e) => patch({ implicitTls: e.target.checked })}
        />
        {t("Implicit TLS")}
      </label>
      <label>
        <input
          type="checkbox"
          checked={value.allowInsecureLoopback}
          onChange={(e) => patch({ allowInsecureLoopback: e.target.checked })}
        />
        {t("Allow insecure local SMTP")}
      </label>
      </div>
      <button type="button" onClick={save}>
        {t("Save SMTP")}
      </button>
      <label>
        {t("Local test recipient")}{" "}
        <input
          type="email" autoComplete="off" value={recipient}
          onChange={(e) => setRecipient(e.target.value)}
        />
      </label>
      <button type="button" className="secondary" disabled={!recipient.trim()} onClick={() => test(recipient.trim())}>
        {t("Send test")}
      </button>
    </fieldset>
  );
}
