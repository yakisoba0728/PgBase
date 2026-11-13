import { providerPresets } from "./presets";
import { describe, expect, it } from "vitest";
import { precondition, type AuthCollectionField, type AuthPolicy, type AuthRecord } from "../api/authCollections";
import { matchesUser, policyDraft, policyInput, policyProblems, profileChanges, profileDraft, providerDraft, providerInput, providerProblems, templateProblems, tokenProblems, uuidBefore, validEndpoint } from "./forms";

const policy: AuthPolicy = {
  passwordEnabled: true,
  minimumPasswordBytes: 12,
  maximumPasswordBytes: 1024,
  passwordPattern: "",
  identityFields: ["email"],
  mfaEnabled: true,
  mfaDurationSeconds: 1800,
  mfaRule: "",
  otpEnabled: false,
  otpDurationSeconds: 300,
  otpMaxAttempts: 5,
  etag: "\"p1\"",
};

describe("policy", () => {
  it("round-trips the stored policy and sends every field", () => {
    const draft = policyDraft(policy, true);
    expect(policyProblems(draft)).toEqual({});
    const { etag: _etag, ...input } = policy;
    expect(policyInput(draft)).toEqual(input);
  });

  it("mirrors the server bounds", () => {
    const draft = { ...policyDraft(policy, true), minimumPasswordBytes: "8", maximumPasswordBytes: "5000", passwordPattern: "(", identityFields: "email, email", otpMaxAttempts: "11", mfaRule: "{" };
    expect(policyProblems(draft)).toEqual({ minimumPasswordBytes: "range", maximumPasswordBytes: "range", passwordPattern: "pattern", identityFields: "name", otpMaxAttempts: "range", mfaRule: "json" });
  });
});

describe("providers", () => {
  it("starts from a catalog preset and sends the whole form, secret only when typed", () => {
    const draft = { ...providerDraft({ name: "github", authorizationUrl: "https://a.test/auth", tokenUrl: "https://a.test/token", userInfoUrl: "https://a.test/user", scopes: ["user:email"] }), clientId: " id " };
    expect(providerProblems(draft)).toEqual({});
    const input = providerInput(draft);
    expect(input).toMatchObject({ clientId: "id", scopes: ["user:email"], redirectUris: [], enabled: true, subjectField: "" });
    expect(input).not.toHaveProperty("clientSecret");
    expect(providerInput({ ...draft, clientSecret: "s" })).toHaveProperty("clientSecret", "s");
  });

  it("requires https except loopback http when allowed, and userinfo or jwks", () => {
    expect(validEndpoint("http://127.0.0.1:9/cb", true)).toBe(true);
    expect(validEndpoint("http://127.0.0.1:9/cb", false)).toBe(false);
    expect(validEndpoint("https://x.test/#frag", false)).toBe(false);
    const draft = { ...providerDraft({ name: "Bad Name" }), authorizationUrl: "http://example.com" };
    expect(providerProblems(draft)).toEqual({ name: "name", clientId: "required", authorizationUrl: "url", tokenUrl: "required", userInfoUrl: "both" });
  });
});

describe("templates and tokens", () => {
  it("checks placeholders per purpose", () => {
    expect(templateProblems("otp", { subject: "Code", body: "Use {{otp}} ({{otp_id}})", htmlBody: "" })).toEqual({});
    expect(templateProblems("verify", { subject: "a\nb", body: "no link", htmlBody: "<p>{{action_url}} {{x}}</p>" })).toEqual({ subject: "range", body: "placeholder", htmlBody: "braces" });
  });

  it("rotates key and version together", () => {
    expect(tokenProblems({ ttlSeconds: "3600", key: "", keyVersion: "" }, true)).toEqual({});
    expect(tokenProblems({ ttlSeconds: "", key: "", keyVersion: "" }, false)).toEqual({});
    expect(tokenProblems({ ttlSeconds: "30", key: "short", keyVersion: "" }, true)).toEqual({ ttlSeconds: "range", key: "short", keyVersion: "required" });
  });
});

describe("users", () => {
  const record = (extra: Partial<AuthRecord>): AuthRecord => ({ id: "r", authCollectionId: "c", email: "a@x.test", emailVisibility: false, verified: false, active: true, fields: {}, createdAt: "2026-01-01T00:00:00.123Z", updatedAt: "", ...extra });

  it("matches by id or by email and creation time", () => {
    const user = { id: "u1", email: "a@x.test", createdAt: "2026-01-01T00:00:00.123Z" };
    expect(matchesUser(record({ id: "u1", email: "other@x.test" }), user)).toBe(true);
    expect(matchesUser(record({ id: "text-id" }), user)).toBe(true);
    expect(matchesUser(record({ id: "text-id", createdAt: "2026-01-02T00:00:00Z" }), user)).toBe(false);
  });

  it("sends only changed, typed profile fields", () => {
    const field = (name: string, type: AuthCollectionField["type"], required = false): AuthCollectionField => ({ name, type, required, unique: false, indexed: false });
    const fields = [field("nick", "text", true), field("age", "integer"), field("tags", "jsonb"), field("pro", "boolean")];
    const initial = profileDraft(fields, { nick: "a", age: 3, tags: ["x"], pro: false });
    expect(initial).toEqual({ nick: "a", age: "3", tags: '[\n  "x"\n]', pro: false });
    expect(profileChanges(fields, { ...initial, age: "4", pro: true }, initial)).toEqual({ changes: { age: 4, pro: true }, problems: {} });
    expect(profileChanges(fields, { ...initial, nick: "", age: "1.5", tags: "[" }, initial).problems).toEqual({ nick: "required", age: "range", tags: "json" });
  });
});

describe("uuidBefore", () => {
  it("returns the previous UUID across borrows and rejects non-UUIDs", () => {
    expect(uuidBefore("0190a1b2-0000-7000-8000-000000000010")).toBe("0190a1b2-0000-7000-8000-00000000000f");
    expect(uuidBefore("0190A1B2-0000-7000-8000-000000000000")).toBe("0190a1b2-0000-7000-7fff-ffffffffffff");
    expect(uuidBefore("0190a1b2-0000-0000-0000-000000000000")).toBe("0190a1b1-ffff-ffff-ffff-ffffffffffff");
    expect(uuidBefore("00000000-0000-0000-0000-000000000000")).toBeUndefined();
    expect(uuidBefore("not-a-uuid")).toBeNull();
  });
});

describe("provider presets", () => {
  it("pass the provider validator once a client id is set, and the server catalog wins", () => {
    const merged = providerPresets([{ name: "google", authorizationUrl: "https://a.example/auth", tokenUrl: "https://a.example/token", userInfoUrl: "https://a.example/me", scopes: ["openid"] }]);
    for (const preset of merged) expect(providerProblems({ ...providerDraft(preset), clientId: "id" })).toEqual({});
    expect(merged.find((preset) => preset.name === "google")?.tokenUrl).toBe("https://a.example/token");
    expect(new Set(merged.map((preset) => preset.name)).size).toBe(merged.length);
  });
});

describe("write preconditions", () => {
  it("sends If-Match for a loaded etag, If-None-Match for a value not stored yet, nothing when forced", () => {
    expect(precondition('"abc"')).toEqual({ "If-Match": '"abc"' });
    expect(precondition(null)).toEqual({ "If-None-Match": "*" });
    expect(precondition(undefined)).toBeUndefined();
  });
});
