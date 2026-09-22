import assert from "node:assert/strict";
import { test } from "node:test";
import { PgBaseClient, PgBaseError } from "../dist/index.js";

const session = { token: "opaque-session", user: { id: "one", email: "a@example.test", createdAt: "now" }, expiresAt: "later" };

test("signup profile goes in body fields while existing fields projection and signal remain independent", async () => {
  const calls = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (url, init) => {
    calls.push({ url: new URL(url), init, body: JSON.parse(init.body) });
    return Response.json(session);
  } });
  const signal = new AbortController().signal;
  const profile = { displayName: "Ada", preferences: { theme: "dark" }, newsletter: false };
  for (const auth of [client, client.realm("members")]) {
    await auth.register("a@example.test", "password", { profile, fields: "id,email" }, signal);
    assert.deepEqual(calls.at(-1).body, { email: "a@example.test", password: "password", fields: profile });
    assert.equal(calls.at(-1).url.searchParams.get("fields"), "id,email");
    assert.equal(calls.at(-1).init.signal, signal);
    await auth.register("a@example.test", "password", signal);
    assert.equal("fields" in calls.at(-1).body, false);
    await auth.login("a@example.test", "password", { profile });
    assert.equal("fields" in calls.at(-1).body, false);
  }
  assert.equal(client.auth.token, session.token);
});

test("OAuth completion helpers carry profile only to completion and retain schema errors", async () => {
  const calls = [];
  const client = new PgBaseClient("http://pgbase.test", { fetch: async (url, init) => {
    const body = JSON.parse(init.body);
    calls.push({ url: new URL(url), body });
    if (String(url).includes("/start")) return Response.json({ authorizationUrl: "https://provider.test?state=expected", expiresAt: "later" });
    if (body.fields?.displayName === "invalid") return Response.json({ error: { code: "validation_failed", details: { displayName: ["required"] } } }, { status: 400 });
    return Response.json(session);
  } });
  const realm = client.realm("members");
  const options = { profile: { displayName: "Ada" }, fields: "id,email" };
  await realm.oauthComplete("github", "expected", "code", options);
  assert.deepEqual(calls.at(-1).body.fields, options.profile);
  assert.equal(calls.at(-1).url.searchParams.get("fields"), options.fields);
  await realm.oauthCompleteFromURL("github", "https://app.test/callback?state=expected&code=code", options);
  assert.deepEqual(calls.at(-1).body.fields, options.profile);
  let closed = false;
  await realm.oauthWithBrowser("github", "https://app.test/callback", {
    open() {}, waitForCallback: async () => "https://app.test/callback?state=expected&code=code", close() { closed = true; },
  }, options);
  assert.equal(closed, true);
  assert.equal("fields" in calls.at(-2).body, false);
  assert.deepEqual(calls.at(-1).body.fields, options.profile);
  await assert.rejects(realm.oauthComplete("github", "expected", "code", { profile: { displayName: "invalid" } }),
    (error) => error instanceof PgBaseError && error.code === "validation_failed" && error.details.displayName[0] === "required");
  assert.equal(client.auth.token, session.token);
});
