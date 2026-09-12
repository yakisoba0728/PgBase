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
          value={value.minimumPasswordBytes}
          onChange={(e) =>
            patch({ minimumPasswordBytes: Number(e.target.value) })
          }
        />
      </label>
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
          value={value.otpMaxAttempts}
          onChange={(e) => patch({ otpMaxAttempts: Number(e.target.value) })}
        />
      </label>
      <button type="button" onClick={save}>
        Save policy
      </button>
    </fieldset>
  );
}
