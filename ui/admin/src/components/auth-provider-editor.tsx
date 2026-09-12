import { useState } from "react";
import type { OAuthProviderSettings } from "../backend/ports";
import { providerPresets } from "../oauth-provider-presets";

export const emptyProvider = (): OAuthProviderSettings => ({
  name: "",
  enabled: true,
  authorizationUrl: "",
  tokenUrl: "",
  userInfoUrl: "",
  clientId: "",
  clientSecret: "",
  secretConfigured: false,
  scopes: [],
  redirectUris: [],
  subjectField: "sub",
  emailField: "email",
  emailVerifiedField: "email_verified",
  avatarField: "picture",
  allowInsecureLoopback: false,
});

type AppleInput = {
  teamId: string;
  keyId: string;
  privateKey: string;
  duration: string;
};

export function ProviderEditor({
  providers,
  value,
  disabled,
  change,
  save,
  remove,
  generateAppleSecret,
}: {
  providers: OAuthProviderSettings[];
  value: OAuthProviderSettings;
  disabled: boolean;
  change(value: OAuthProviderSettings): void;
  save(): void;
  remove(name: string): void;
  generateAppleSecret(
    input: AppleInput,
    reveal: (secret: string) => void,
  ): void;
}) {
  const [apple, setApple] = useState<AppleInput>({
    teamId: "",
    keyId: "",
    privateKey: "",
    duration: "15777000",
  });
  const [appleSecret, setAppleSecret] = useState("");
  const patch = (next: Partial<OAuthProviderSettings>) =>
    change({ ...value, ...next });
  function generate() {
    generateAppleSecret(apple, (secret) => {
      setAppleSecret(secret);
      patch({ clientSecret: secret });
      setApple((current) => ({ ...current, privateKey: "" }));
    });
  }
  return (
    <fieldset disabled={disabled}>
      <legend>OAuth providers</legend>
      {providers.map((item) => (
        <div key={item.name}>
          <strong>{item.name}</strong> ({item.enabled ? "enabled" : "disabled"}){" "}
          <button
            type="button"
            onClick={() => change({ ...item, clientSecret: "" })}
          >
            Edit
          </button>{" "}
          <button type="button" onClick={() => remove(item.name)}>
            Remove
          </button>
        </div>
      ))}
      <label>
        Provider preset{" "}
        <select
          value=""
          onChange={(e) => {
            const preset = providerPresets.find(
              (item) => item.value.name === e.target.value,
            );
            if (preset) change({ ...emptyProvider(), ...preset.value });
          }}
        >
          <option value="">Custom OAuth 2.0 / OIDC</option>
          {providerPresets.map((item) => (
            <option key={item.value.name} value={item.value.name}>
              {item.label}
            </option>
          ))}
        </select>
      </label>
      <label>
        Name{" "}
        <input
          value={value.name}
          onChange={(e) => patch({ name: e.target.value })}
        />
      </label>
      <label>
        Authorization URL{" "}
        <input
          value={value.authorizationUrl}
          onChange={(e) => patch({ authorizationUrl: e.target.value })}
        />
      </label>
      <label>
        Token URL{" "}
        <input
          value={value.tokenUrl}
          onChange={(e) => patch({ tokenUrl: e.target.value })}
        />
      </label>
      <label>
        User info URL{" "}
        <input
          value={value.userInfoUrl}
          onChange={(e) => patch({ userInfoUrl: e.target.value })}
        />
      </label>
      <label>
        Client ID{" "}
        <input
          value={value.clientId}
          onChange={(e) => patch({ clientId: e.target.value })}
        />
      </label>
      <label>
        Client secret{" "}
        <input
          type="password"
          placeholder={
            value.secretConfigured ? "Stored; leave blank to keep" : ""
          }
          value={value.clientSecret ?? ""}
          onChange={(e) => patch({ clientSecret: e.target.value })}
        />
      </label>
      {value.name === "apple" && (
        <fieldset>
          <legend>Apple client secret</legend>
          <p>
            Generate a signed client secret from your Apple team signing key,
            then save this provider.
          </p>
          <label>
            Team ID{" "}
            <input
              value={apple.teamId}
              onChange={(e) => setApple({ ...apple, teamId: e.target.value })}
            />
          </label>
          <label>
            Key ID{" "}
            <input
              value={apple.keyId}
              onChange={(e) => setApple({ ...apple, keyId: e.target.value })}
            />
          </label>
          <label>
            Private key{" "}
            <textarea
              value={apple.privateKey}
              onChange={(e) =>
                setApple({ ...apple, privateKey: e.target.value })
              }
            />
          </label>
          <label>
            Lifetime in seconds{" "}
            <input
              min={1}
              max={15777000}
              type="number"
              value={apple.duration}
              onChange={(e) => setApple({ ...apple, duration: e.target.value })}
            />
          </label>
          <button type="button" onClick={generate}>
            Generate Apple client secret
          </button>
          {appleSecret && (
            <label>
              Generated Apple client secret
              <textarea
                aria-label="Generated Apple client secret"
                readOnly
                value={appleSecret}
              />
            </label>
          )}
        </fieldset>
      )}
      <label>
        Scopes (space separated){" "}
        <input
          value={value.scopes.join(" ")}
          onChange={(e) =>
            patch({ scopes: e.target.value.split(/\s+/).filter(Boolean) })
          }
        />
      </label>
      <label>
        Avatar URL field{" "}
        <input
          value={value.avatarField}
          onChange={(e) => patch({ avatarField: e.target.value })}
        />
      </label>
      <label>
        Redirect URIs (one per line){" "}
        <textarea
          value={value.redirectUris.join("\n")}
          onChange={(e) =>
            patch({
              redirectUris: e.target.value
                .split("\n")
                .map((v) => v.trim())
                .filter(Boolean),
            })
          }
        />
      </label>
      <label>
        <input
          type="checkbox"
          checked={value.enabled}
          onChange={(e) => patch({ enabled: e.target.checked })}
        />
        Enabled
      </label>
      <label>
        <input
          type="checkbox"
          checked={value.allowInsecureLoopback}
          onChange={(e) => patch({ allowInsecureLoopback: e.target.checked })}
        />
        Allow HTTP for loopback development endpoints
      </label>
      <button type="button" onClick={save}>
        Save provider
      </button>
    </fieldset>
  );
}
