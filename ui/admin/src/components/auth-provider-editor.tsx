import { useEffect, useState } from "react";
import type { OAuthProviderSettings } from "../backend/ports";
import { useBackend } from "../auth/session";
import { providerPresets } from "../oauth-provider-presets";

export const emptyProvider = (): OAuthProviderSettings => ({
  name: "",
  enabled: true,
  authorizationUrl: "",
  tokenUrl: "",
  userInfoUrl: "",
  issuer: "", jwksUrl: "",
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
  const { authCollections } = useBackend();
  const [catalog, setCatalog] = useState(providerPresets);
  const [catalogError, setCatalogError] = useState(false);
  useEffect(() => {
    if (!authCollections.providerCatalog) return;
    const c = new AbortController();
    void authCollections.providerCatalog(c.signal).then(items => {
      if (!c.signal.aborted) setCatalog([...items.map(value => ({ label: providerPresets.find(p => p.value.name === value.name)?.label ?? value.name, value: { ...providerPresets.find(p => p.value.name === value.name)?.value, ...value } })), ...providerPresets.filter(p => !items.some(item => item.name === p.value.name))]);
    }).catch(() => { if (!c.signal.aborted) setCatalogError(true); });
    return () => c.abort();
  }, [authCollections]);
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
            const preset = catalog.find(
              (item) => item.value.name === e.target.value,
            );
            if (preset) change({ ...emptyProvider(), ...preset.value });
          }}
        >
          <option value="">Custom OAuth 2.0 / OIDC</option>
          {catalog.map((item) => (
            <option key={item.value.name} value={item.value.name}>
              {item.label}
            </option>
          ))}
        </select>
      </label>
      {catalogError && <p role="status">Server provider presets are unavailable. You can still enter a custom provider.</p>}
      <div className="settings-field-grid">
        <label>OIDC issuer<input value={value.issuer ?? ""} onChange={e => patch({ issuer: e.target.value })} placeholder="https://identity.example.com" /></label>
        <label>JWKS URL<input value={value.jwksUrl ?? ""} onChange={e => patch({ jwksUrl: e.target.value })} placeholder="https://identity.example.com/.well-known/jwks.json" /></label>
      </div>
      <p className="settings-help">For OIDC without a user info endpoint, provide the issuer and JWKS URL. Existing OIDC values are preserved when saving.</p>
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
