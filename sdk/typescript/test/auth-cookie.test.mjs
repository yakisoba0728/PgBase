import assert from "node:assert/strict";
import test from "node:test";
import { AuthStore, PgBaseError } from "../dist/index.js";

const invalid = (action) => assert.throws(action, (error) => error instanceof PgBaseError && error.code === "invalid_cookie_option");

test("native namespace, explicit defaults and lossless round trip", () => {
  const source = new AuthStore();
  source.save("opaque;token=with unicode 한글", { id: "u", note: "quoted \" text" });
  const cookie = source.exportToCookie();
  assert.ok(cookie.startsWith("pgbase_auth="));
  assert.ok(cookie.endsWith("; Path=/; HttpOnly; SameSite=Lax"));
  const restored = new AuthStore();
  restored.loadFromCookie(cookie);
  assert.equal(restored.token, source.token);
  assert.deepEqual(restored.record, source.record);
});

test("legacy namespace is explicit and never imported implicitly", () => {
  const source = new AuthStore();
  source.save("legacy");
  const cookie = source.exportToCookie({ key: "pb_auth" });
  const target = new AuthStore();
  target.loadFromCookie(cookie);
  assert.equal(target.token, null);
  target.loadFromCookie(cookie, "pb_auth");
  assert.equal(target.token, "legacy");
});

test("rejects all control characters in cookie names and paths", () => {
  const store = new AuthStore();
  for (const code of [...Array(32).keys(), 127]) {
    const control = String.fromCharCode(code);
    invalid(() => store.exportToCookie({ key: `name${control}` }));
    invalid(() => store.loadFromCookie("", `name${control}`));
    invalid(() => store.exportToCookie({ path: `/app${control}` }));
  }
  for (const key of ["", "a=b", "a;b", "a b", "한글"]) invalid(() => store.exportToCookie({ key }));
  for (const path of ["", "relative", "/; Secure", "/한글"]) invalid(() => store.exportToCookie({ path }));
});

test("validates JavaScript caller options and cross-site policy", () => {
  const store = new AuthStore();
  for (const options of [{ sameSite: "None" }, { sameSite: "Lax; Secure" }, { sameSite: "lax" }, { sameSite: "Lax\r\nInjected: yes" }, { secure: "yes" }, { httpOnly: "no" }]) {
    invalid(() => store.exportToCookie(options));
  }
  assert.ok(store.exportToCookie({ secure: true, sameSite: "None" }).endsWith("HttpOnly; Secure; SameSite=None"));
  assert.ok(store.exportToCookie({ httpOnly: false, sameSite: "Strict", path: "/app" }).endsWith("Path=/app; SameSite=Strict"));
});

test("protected cookie prefixes require their associated attributes", () => {
  const store = new AuthStore();
  for (const key of ["__Secure-session", "__Host-session", "__Http-session", "__Host-Http-session"]) {
    invalid(() => store.exportToCookie({ key }));
    assert.ok(store.exportToCookie({ key, secure: true }).startsWith(`${key}=`));
  }
  invalid(() => store.exportToCookie({ key: "__Host-session", secure: true, path: "/app" }));
  invalid(() => store.exportToCookie({ key: "__Http-session", secure: true, httpOnly: false }));
  invalid(() => store.exportToCookie({ key: "__Host-Http-session", secure: true, httpOnly: false }));
});

test("malformed cached state is cleared and a null token cannot retain an identity", () => {
  const store = new AuthStore();
  store.save("before", { id: "old" });
  store.loadFromCookie(`pgbase_auth=${encodeURIComponent(JSON.stringify({ token: null, record: { id: "untrusted" } }))}`);
  assert.equal(store.token, null);
  assert.equal(store.record, null);
  store.save("before");
  store.loadFromCookie("pgbase_auth=%invalid");
  assert.equal(store.isValid, false);
});
