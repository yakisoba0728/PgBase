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
  const [recipient, setRecipient] = useState("");
  const patch = (next: Partial<AuthSMTPSettings>) =>
    change({ ...value, ...next });
  return (
    <fieldset disabled={disabled}>
      <legend>SMTP</legend>
      <label>
        Server address{" "}
        <input
          placeholder="localhost:1025"
          value={value.address}
          onChange={(e) => patch({ address: e.target.value })}
        />
      </label>
      <label>
        From{" "}
        <input
          value={value.from}
          onChange={(e) => patch({ from: e.target.value })}
        />
      </label>
      <label>
        Username{" "}
        <input
          value={value.username}
          onChange={(e) => patch({ username: e.target.value })}
        />
      </label>
      <label>
        Password{" "}
        <input
          type="password"
          placeholder={
            value.passwordConfigured ? "Stored; leave blank to keep" : ""
          }
          value={value.password ?? ""}
          onChange={(e) => patch({ password: e.target.value })}
        />
      </label>
      <label>
        Server name{" "}
        <input
          value={value.serverName}
          onChange={(e) => patch({ serverName: e.target.value })}
        />
      </label>
      <label>
        <input
          type="checkbox"
          checked={value.implicitTls}
          onChange={(e) => patch({ implicitTls: e.target.checked })}
        />
        Implicit TLS
      </label>
      <label>
        <input
          type="checkbox"
          checked={value.allowInsecureLoopback}
          onChange={(e) => patch({ allowInsecureLoopback: e.target.checked })}
        />
        Allow insecure local SMTP
      </label>
      <button type="button" onClick={save}>
        Save SMTP
      </button>
      <label>
        Local test recipient{" "}
        <input
          value={recipient}
          onChange={(e) => setRecipient(e.target.value)}
        />
      </label>
      <button type="button" onClick={() => test(recipient)}>
        Send test
      </button>
    </fieldset>
  );
}
