import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import vm from "node:vm";

const source = await readFile(new URL("./app.js", import.meta.url), "utf8");

function deferred() {
  let resolve, reject;
  const promise = new Promise((next, fail) => { resolve = next; reject = fail; });
  return { promise, resolve, reject };
}

async function eventually(check) {
  for (let i = 0; i < 20; i += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  assert.fail("condition was not reached");
}

function loadExample({ logout, list = async () => ({ items: [] }) }) {
  const handlers = new Map();
  const elements = new Map();
  for (const id of ["status", "note-form", "logout", "base-url", "email", "password", "notes", "file-record-id", "note", "reset-email", "action-token", "new-password", "file-input", "file-field", "register", "login", "create", "request-reset", "confirm-reset", "request-verification", "confirm-verification", "request-email-change", "confirm-email-change", "upload-file"]) {
    elements.set(id, {
      value: id === "base-url" ? "https://app.test" : "",
      hidden: id === "note-form",
      disabled: id === "logout",
      textContent: "",
      files: [],
      addEventListener: (_event, handler) => handlers.set(id, handler),
      children: [],
      replaceChildren(...children) { this.children = children; },
    });
  }
  const streams = [];
  class PgBaseClient {
    constructor(baseURL) { this.baseURL = baseURL; }
    async login() { return { user: { email: "person@app.test" } }; }
    async register() { return { user: { email: "person@app.test" } }; }
    async logout() { return logout(); }
    records() {
      return {
        list,
        events: ({ signal }) => {
          const gate = deferred();
          streams.push({ signal, gate });
          return (async function* () {
            const event = await gate.promise;
            if (event) yield event;
          })();
        },
        create: async () => {},
      };
    }
    files() { return { upload: async () => {} }; }
  }
  const context = {
    AbortController,
    URL,
    PgBaseClient,
    document: {
      getElementById: (id) => elements.get(id),
      createElement: () => ({ append: () => {}, onclick: undefined, textContent: "" }),
    },
  };
  vm.runInNewContext(source.replace('import { PgBaseClient } from "/sdk/index.js";', ''), context);
  return { handlers, elements, streams };
}

{
  const page = loadExample({ logout: async () => { throw { code: "offline" }; } });
  await page.handlers.get("login")();
  await eventually(() => page.streams.length === 1);
  await page.handlers.get("logout")();
  assert.equal(page.elements.get("status").textContent, "Sign out failed: offline");
  assert.equal(page.elements.get("logout").disabled, true);
  assert.equal(page.elements.get("note-form").hidden, true);
  assert.equal(page.streams.length, 1);
  assert.equal(page.streams[0].signal.aborted, true);
}

{
  const page = loadExample({ logout: async () => {} });
  await page.handlers.get("login")();
  await eventually(() => page.streams.length === 1);
  const first = page.streams[0];
  await page.handlers.get("login")();
  await eventually(() => page.streams.length === 2);
  first.gate.reject(new Error("late old stream failure"));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.doesNotMatch(page.elements.get("status").textContent, /Live updates stopped/);

  page.streams[1].gate.reject({ code: "network_lost" });
  await eventually(() => page.elements.get("status").textContent === "Live updates stopped: network_lost");
}


{
  let calls = 0;
  const page = loadExample({ logout: async () => {}, list: async () => {
    if (++calls === 1) return { items: [{ id: "old", title: "Old note" }] };
    throw { code: "unavailable" };
  } });
  await page.handlers.get("login")();
  assert.equal(page.elements.get("notes").children.length, 1);
  page.elements.get("note").value = "prior draft";
  page.elements.get("file-record-id").value = "old";
  await page.handlers.get("logout")();
  await page.handlers.get("login")();
  assert.equal(page.elements.get("status").textContent, "Could not load notes: unavailable");
  assert.equal(page.elements.get("note-form").hidden, true);
  assert.equal(page.elements.get("notes").children.length, 0);
  assert.equal(page.elements.get("note").value, "");
  assert.equal(page.elements.get("file-record-id").value, "");
}
