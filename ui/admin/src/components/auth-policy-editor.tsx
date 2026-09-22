import type { AuthPolicy } from "../backend/ports";

export function PolicyEditor({
  value,
  disabled,
  change,
  save,
}: {
  value: AuthPolicy;
  disabled: boolean;
  change(value: AuthPolicy): void;
  save(): void;
}) {
  const patch = (next: Partial<AuthPolicy>) => change({ ...value, ...next });
  return (
    <form onSubmit={(event) => { event.preventDefault(); save(); }}>
    <fieldset disabled={disabled}>
      <legend>Password and MFA policy</legend>
      <label>
        <input
          type="checkbox"
          checked={value.passwordEnabled}
          onChange={(e) => patch({ passwordEnabled: e.target.checked })}
        />
        Password authentication
      </label>
      <label>
        Minimum password bytes{" "}
        <input
          type="number"
          min={12}
          max={128}
          required
          value={value.minimumPasswordBytes}
          onChange={(e) =>
            patch({ minimumPasswordBytes: Number(e.target.value) })
          }
        />
      </label>
      <label>
        Maximum password bytes{" "}
        <input type="number" min={value.minimumPasswordBytes} max={4096} required value={value.maximumPasswordBytes} onChange={(e) => patch({ maximumPasswordBytes: Number(e.target.value) })} />
      </label>
      <p>Password lengths count UTF-8 bytes.</p>
      <label>
        Password pattern{" "}
        <input value={value.passwordPattern} onChange={(e) => patch({ passwordPattern: e.target.value })} />
      </label>
      <p>Optional Go regular expression checked by the server. Leave blank for no pattern requirement.</p>
      <label>
        Login identity fields{" "}
        <textarea required value={value.identityFields.join("\n")} onChange={(e) => patch({ identityFields: e.target.value.split("\n") })} />
      </label>
      <p>One field per line, up to 8: email or visible text profile fields. Only listed fields can be used to sign in; existing nonempty values must be unique.</p>
      <label>
        <input
          type="checkbox"
          checked={value.mfaEnabled}
          onChange={(e) => patch({ mfaEnabled: e.target.checked })}
        />
        TOTP and recovery codes
      </label>
      <p>
        Disabling TOTP deletes enrolled factors and recovery codes in this
        realm. Re-enabling requires users to enroll again.
      </p>
      <label>
        MFA challenge lifetime in seconds{" "}
        <input type="number" min={60} max={86400} required value={value.mfaDurationSeconds} onChange={(e) => patch({ mfaDurationSeconds: Number(e.target.value) })} />
      </label>
      <label>
        MFA rule JSON{" "}
        <textarea value={value.mfaRule} onChange={(e) => patch({ mfaRule: e.target.value })} placeholder={'{"field":"verified","equals":true}'} />
      </label>
      <p>For users with an enrolled factor, blank always requires MFA. A rule compares verified or a profile field with a JSON scalar, for example {`{"field":"verified","equals":true}`}. The server validates this rule.</p>
      <label>
        <input
          type="checkbox"
          checked={value.otpEnabled}
          onChange={(e) => patch({ otpEnabled: e.target.checked })}
        />
        Email one-time-code login
      </label>
      <label>
        OTP lifetime in seconds{" "}
        <input
          type="number"
          min={60}
          max={900}
          required
          value={value.otpDurationSeconds}
          onChange={(e) =>
            patch({ otpDurationSeconds: Number(e.target.value) })
          }
        />
      </label>
      <label>
        OTP attempts{" "}
        <input
          type="number"
          min={1}
          max={10}
          required
          value={value.otpMaxAttempts}
          onChange={(e) => patch({ otpMaxAttempts: Number(e.target.value) })}
        />
      </label>
      <button type="submit">
        Save policy
      </button>
    </fieldset>
    </form>
  );
}
