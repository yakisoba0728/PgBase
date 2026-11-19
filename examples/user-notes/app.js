import { PgBaseClient } from "/sdk/index.js";

const byID = (id) => document.getElementById(id);
const status = byID("status");
const form = byID("note-form");
const logout = byID("logout");
let client;
let eventAbort;
let generation = 0;

function connect() {
  const baseURL = byID("base-url").value.trim();
  if (!client || new URL(client.baseURL).href !== new URL(baseURL).href) {
    clearNotes();
    client = new PgBaseClient(baseURL);
  }
  return client;
}

function message(value) {
  status.textContent = value;
}

function clearNotes() {
  generation++;
  eventAbort?.abort();
  form.hidden = true;
  logout.disabled = true;
  byID("notes").replaceChildren();
  for (const id of ["note", "file-record-id", "file-input"]) byID(id).value = "";
}

async function authenticate(method) {
  let active = generation;
  try {
    const current = connect();
    clearNotes();
    active = generation;
    const session = await current[method](byID("email").value, byID("password").value);
    if (active !== generation) return;
    message(`Signed in as ${session.user.email}`);
    logout.disabled = false;
    await loadNotes(current, active);
    if (active === generation) startEvents(current, active);
  } catch (error) {
    if (active === generation) message(`Request failed: ${error.code ?? error.message}`);
  }
}

async function loadNotes(current, active) {
  try {
    const page = await current.records("notes").list({ sort: "-created_at", limit: 50 });
    if (active !== generation) return;
    byID("notes").replaceChildren(...page.items.map((note) => {
      const item = document.createElement("li");
      item.textContent = `${note.title ?? ""} (${note.id})`;
      const files = document.createElement("button"); files.textContent = "Choose for file upload";
      files.onclick = () => { byID("file-record-id").value = note.id; };
      item.append(files);
      return item;
    }));
    form.hidden = false;
  } catch (error) {
    if (active === generation) message(`Could not load notes: ${error.code ?? error.message}`);
  }
}

byID("register").addEventListener("click", () => authenticate("register"));
byID("login").addEventListener("click", () => authenticate("login"));
logout.addEventListener("click", async () => {
  const current = client;
  clearNotes();
  const active = generation;
  try {
    await current.logout();
    if (active === generation) message("Signed out");
  } catch (error) {
    if (active === generation) message(`Sign out failed: ${error.code ?? error.message}`);
  }
});
byID("create").addEventListener("click", async () => {
  const current = client, active = generation;
  try {
    const text = byID("note").value.trim();
    if (!text) return;
    await current.records("notes").create({ title: text }, crypto.randomUUID());
    if (active !== generation) return;
    byID("note").value = "";
    await loadNotes(current, active);
  } catch (error) {
    if (active === generation) message(`Could not save note: ${error.code ?? error.message}`);
  }
});

function startEvents(current, active) {
  eventAbort?.abort();
  const controller = new AbortController();
  eventAbort = controller;
  (async () => {
    try {
      for await (const event of current.records("notes").events({ signal: controller.signal })) {
        if (active !== generation) break;
        message(`Note ${event.operation}; refreshing private list`);
        await loadNotes(current, active);
      }
    } catch (error) {
      if (!controller.signal.aborted && active === generation) message(`Live updates stopped: ${error.code ?? error.message}`);
    }
  })();
}

byID("request-reset").addEventListener("click", async () => {
  try {
    await connect().requestPasswordReset(byID("reset-email").value);
    message("Password-reset request accepted");
  } catch (error) { message(`Password reset failed: ${error.code ?? error.message}`); }
});
byID("confirm-reset").addEventListener("click", async () => {
  try { await connect().confirmPasswordReset(byID("action-token").value, byID("new-password").value); message("Password reset; log in with the new password"); }
  catch (error) { message(`Password reset failed: ${error.code ?? error.message}`); }
});
byID("request-verification").addEventListener("click", async () => {
  try { await client.requestVerification(); message("Verification request accepted"); }
  catch (error) { message(`Verification request failed: ${error.code ?? error.message}`); }
});
byID("confirm-verification").addEventListener("click", async () => {
  try {
    await client.confirmVerification(byID("action-token").value);
    message("Email verified");
  } catch (error) { message(`Verification failed: ${error.code ?? error.message}`); }
});
byID("request-email-change").addEventListener("click", async () => {
  try {
    await client.requestEmailChange(byID("password").value, byID("new-email").value);
    message("Email-change request accepted");
  } catch (error) { message(`Email change failed: ${error.code ?? error.message}`); }
});
byID("confirm-email-change").addEventListener("click", async () => {
  const current = client;
  if (!current) return;
  clearNotes();
  const active = generation;
  try {
    await current.confirmEmailChange(byID("action-token").value);
    if (active === generation) message("Email changed; sign in with the new address");
  } catch (error) {
    if (active === generation) message(`Email confirmation failed: ${error.code ?? error.message}`);
  }
});
byID("upload-file").addEventListener("click", async () => {
  try {
    const file = byID("file-input").files[0], recordID = byID("file-record-id").value.trim(), field = byID("file-field").value.trim();
    if (!file || !recordID || !field) { message("Choose a file, note ID, and file field"); return; }
    await client.files("notes", recordID).upload(field, file, file.name);
    message("File uploaded");
  } catch (error) { message(`File upload failed: ${error.code ?? error.message}`); }
});
