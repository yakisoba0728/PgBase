import assert from "node:assert/strict";
import { test } from "node:test";
import { AuthStore, PgBaseClient, PgBaseError } from "../dist/index.js";

const changed = (error) => error instanceof PgBaseError && error.status === 403 && error.code === "session_changed";

for (const kind of ["parsed", "exact", "write", "multipart"]) {
  for (const outcome of ["success", "HTTP error", "transport error"]) {
    test(`user ${kind} rejects an old ${outcome} body after shared auth changes`, async () => {
      const auth = new AuthStore();
      auth.save("old-user");
      let entered, body, sentToken;
      const started = new Promise((resolve) => { entered = resolve; });
      const response = new Response(new ReadableStream({ start(controller) { body = controller; } }), {
        status: outcome === "HTTP error" ? 401 : 200,
      });
      const read = response.text.bind(response);
      response.text = () => { entered(); return read(); };
      const client = new PgBaseClient("http://pgbase.test", { auth, fetch: async (_url, init) => {
        sentToken = new Headers(init.headers).get("authorization");
        return response;
      } });
      const other = new PgBaseClient("http://pgbase.test", { auth });
      const pending = kind === "parsed" ? client.records("notes").get("one")
        : kind === "exact" ? client.records("notes").getJSON("one")
        : kind === "write" ? client.records("notes").create({ title: "private" }, "create-old-user")
        : client.files("notes", "one").upload("attachment", new Blob(["private"]), "private.txt");
      const rejected = assert.rejects(pending, changed);
      await started;
      other.auth.save("new-user");
      if (outcome === "transport error") body.error(new TypeError("old connection lost"));
      else {
        body.enqueue(new TextEncoder().encode(outcome === "success" ? '{"id":"private-old-user"}' : '{"error":{"code":"unauthorized"}}'));
        body.close();
      }
      await rejected;
      assert.equal(sentToken, "Bearer old-user");
      assert.equal(auth.token, "new-user");
    });
  }
}

test("file preparation cannot submit under a replacement user's token", async () => {
  let release, calls = 0;
  const bytes = new Promise((resolve) => { release = resolve; });
  const file = new Blob(["private"]);
  file.arrayBuffer = () => bytes;
  const client = new PgBaseClient("http://pgbase.test", { fetch: async () => {
    calls++;
    return Response.json({ id: "file" });
  } });
  client.auth.save("old-user");
  const rejected = assert.rejects(client.files("notes", "one").upload("attachment", file, "private.txt"), changed);
  client.auth.save("new-user");
  release(new TextEncoder().encode("private").buffer);
  await rejected;
  assert.equal(calls, 0);
});

for (const realm of [false, true]) {
  test(`a stale ${realm ? "realm" : "public"} login cannot return its obsolete session`, async () => {
    let release;
    const client = new PgBaseClient("http://pgbase.test", { fetch: () => new Promise((resolve) => { release = resolve; }) });
    const pending = realm ? client.realm("members").login("old@example.test", "password") : client.login("old@example.test", "password");
    const rejected = assert.rejects(pending, changed);
    client.auth.save("new-user");
    release(Response.json({ token: "old-token", user: { id: "old-user", email: "old@example.test", createdAt: "then" }, expiresAt: "later" }));
    await rejected;
    assert.equal(client.auth.token, "new-user");
  });
}
