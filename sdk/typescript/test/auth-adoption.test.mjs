import assert from "node:assert/strict";
import { test } from "node:test";
import { AuthStore, PgBaseClient, PgBaseError } from "../dist/index.js";

const changed = (error) => error instanceof PgBaseError && error.status === 403 && error.code === "session_changed";
const session = (id) => ({ token: `token-${id}`, user: { id, email: `${id}@example.test`, createdAt: "then" }, expiresAt: "later" });
const completions = [
  ["public login", (client) => client.login("member@example.test", "password")],
  ["public registration", (client) => client.register("member@example.test", "password")],
  ["realm login", (client) => client.realm("members").login("member@example.test", "password")],
  ["realm registration", (client) => client.realm("members").register("member@example.test", "password")],
  ["public OTP", (client) => client.completeOTP("otp", "123456")],
  ["realm OTP", (client) => client.realm("members").completeOTP("otp", "123456")],
  ["MFA", (client) => client.completeMFA("challenge", "123456")],
  ["OAuth", (client) => client.realm("members").oauthComplete("provider", "state", "code")],
  ["refresh", (client) => client.realm("members").refresh()],
];

for (const [name, complete] of completions) {
  test(`simultaneous ${name} responses cannot return an unadopted session`, async () => {
    const auth = new AuthStore();
    auth.save("initial");
    const release = [];
    const options = { auth, fetch: () => new Promise((resolve) => { release.push(resolve); }) };
    // Refresh is serialized per client; shared stores still need one adoption boundary.
    const one = new PgBaseClient("http://pgbase.test", options);
    const two = name === "refresh" ? new PgBaseClient("http://pgbase.test", options) : one;
    const pending = Promise.allSettled([complete(one), complete(two)]);
    assert.equal(release.length, 2);
    release[0](Response.json(session("a")));
    release[1](Response.json(session("b")));
    const results = await pending;
    const accepted = results.filter((result) => result.status === "fulfilled");
    const rejected = results.filter((result) => result.status === "rejected");
    assert.equal(accepted.length, 1);
    assert.equal(rejected.length, 1);
    assert.equal(accepted[0].value.token, auth.token);
    assert.equal(changed(rejected[0].reason), true);
  });
}

for (const name of ["public login", "refresh"]) {
  for (const failStorage of [false, true]) {
    test(`${name} checks auth again after reentrant persistence (failure=${failStorage})`, async () => {
      let auth;
      auth = new AuthStore({
        get: () => "initial",
        clear() {},
        set(token) {
          if (token !== "token-a") return;
          auth.save("replacement");
          if (failStorage) throw new Error("old storage failure");
        },
      });
      const client = new PgBaseClient("http://pgbase.test", { auth, fetch: async () => Response.json(session("a")) });
      await assert.rejects(completions.find(([label]) => label === name)[1](client), changed);
      assert.equal(auth.token, "replacement");
    });
  }
}

for (const kind of ["record", "upload"]) {
  test(`${kind} checks auth at its final SDK continuation`, async () => {
    const release = [];
    let bothStarted;
    const started = new Promise((resolve) => { bothStarted = resolve; });
    const client = new PgBaseClient("http://pgbase.test", { fetch: () => new Promise((resolve) => {
      release.push(resolve);
      if (release.length === 2) bothStarted();
    }) });
    client.auth.save("initial");
    const login = client.login("b@example.test", "password");
    const pending = kind === "record" ? client.records("notes").get("one")
      : client.files("notes", "one").upload("attachment", new Blob(["private"]), "private.txt");
    const rejected = assert.rejects(pending, changed);
    await started;
    release[0](Response.json(session("b")));
    release[1](Response.json({ id: "old-private" }));
    await Promise.all([login, rejected]);
    assert.equal(client.auth.token, "token-b");
  });
}
