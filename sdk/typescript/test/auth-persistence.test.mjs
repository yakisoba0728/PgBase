import assert from "node:assert/strict";
import { test } from "node:test";
import * as sdk from "../dist/index.js";

test("opt-in browser auth storage restores the token and record together and clears both", () => {
  const entries = new Map();
  const storage = { getItem: (key) => entries.get(key) ?? null, setItem: (key, value) => entries.set(key, value), removeItem: (key) => entries.delete(key) };
  const first = new sdk.AuthStore(sdk.browserAuthStorage(storage));
  first.save("opaque-session", { id: "CaseSensitive01", email: "member@example.test", verified: true });
  const restored = new sdk.AuthStore(sdk.browserAuthStorage(storage));
  assert.equal(restored.token, "opaque-session");
  assert.deepEqual(restored.record, { id: "CaseSensitive01", email: "member@example.test", verified: true });
  assert.equal(restored.isValid, true);
  restored.clear();
  const cleared = new sdk.AuthStore(sdk.browserAuthStorage(storage));
  assert.equal(cleared.token, null);
  assert.equal(cleared.record, null);
});

test("legacy TokenStorage and browserTokenStorage still persist only the opaque token", () => {
  let value = "legacy-token";
  const storage = { get: () => value, set: (token) => { value = token; }, clear: () => { value = null; } };
  const auth = new sdk.AuthStore(storage);
  assert.equal(auth.token, "legacy-token");
  auth.save("replacement-token", { id: "one" });
  assert.equal(value, "replacement-token");
  assert.equal(new sdk.AuthStore(storage).record, null);
  const browser = { getItem: () => value, setItem: (_key, token) => { value = token; }, removeItem: () => { value = null; } };
  new sdk.AuthStore(sdk.browserTokenStorage(browser)).save("browser-token", { id: "two" });
  assert.equal(value, "browser-token");
  auth.clear();
  assert.equal(value, null);
});

test("state storage snapshots restored data and ignores corrupt browser state", () => {
  const state = { token: "session", record: { id: "one", metadata: { visible: true } } };
  const auth = new sdk.AuthStore({ read: () => state, write: () => {}, clear: () => {} });
  state.record.metadata.visible = false;
  assert.equal(auth.record.metadata.visible, true);
  for (const value of ['not-json', '{"token":123,"record":null}', '{"token":"session","record":[]}']) {
    const storage = { getItem: () => value, setItem: () => {}, removeItem: () => {} };
    const restored = new sdk.AuthStore(sdk.browserAuthStorage(storage));
    assert.equal(restored.token, null);
    assert.equal(restored.record, null);
  }
});
