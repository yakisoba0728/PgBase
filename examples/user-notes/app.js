import { PgBaseClient } from "/sdk/index.js";

const byID = (id) => document.getElementById(id);
const status = byID("status");
const form = byID("note-form");
const logout = byID("logout");
let client;
let eventAbort;

function connect() {
  const baseURL = byID("base-url").value.trim();
  if (!client || new URL(client.baseURL).href !== new URL(baseURL).href) {
    eventAbort?.abort();
    client = new PgBaseClient(baseURL);
  }
  return client;
}

function message(value) {
  status.textContent = value;
}

async function authenticate(method) {
  try {
    const session = await connect()[method](byID("email").value, byID("password").value);
    message(`Signed in as ${session.user.email}`);
    form.hidden = false;
    logout.disabled = false;
    await loadNotes();
    startEvents();
  } catch (error) {
    message(`Request failed: ${error.code ?? error.message}`);
  }
}

async function loadNotes() {
  try {
    const page = await client.records("notes").list({ sort: "-created_at", limit: 50 });
    byID("notes").replaceChildren(...page.items.map((note) => {
      const item = document.createElement("li");
      item.textContent = `${note.title ?? ""} (${note.id})`;
      const files = document.createElement("button"); files.textContent = "Choose for file upload";
      files.onclick = () => { byID("file-record-id").value = note.id; };
      item.append(files);
      return item;
    }));
  } catch (error) {
    message(`Could not load notes: ${error.code ?? error.message}`);
  }
}

byID("register").addEventListener("click", () => authenticate("register"));
byID("login").addEventListener("click", () => authenticate("login"));
logout.addEventListener("click", async () => {
  const activeClient = client;
  eventAbort?.abort();
  try {
    await activeClient.logout();
    form.hidden = true;
    logout.disabled = true;
    message("Signed out");
  } catch (error) {
    if (client === activeClient) startEvents();
    message(`Sign out failed: ${error.code ?? error.message}`);
  }
});
byID("create").addEventListener("click", async () => {
  try {
    const text = byID("note").value.trim();
    if (!text) return;
    await client.records("notes").create({ title: text }, crypto.randomUUID());
    byID("note").value = "";
    await loadNotes();
  } catch (error) {
    message(`Could not save note: ${error.code ?? error.message}`);
  }
});

function startEvents() {
  eventAbort?.abort();
  const controller = new AbortController();
  eventAbort = controller;
  (async () => {
    try {
      for await (const event of client.records("notes").events({ signal: controller.signal })) {
        message(`Note ${event.operation}; refreshing private list`);
        await loadNotes();
      }
    } catch (error) {
      if (!controller.signal.aborted) message(`Live updates stopped: ${error.code ?? error.message}`);
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
  try {
    await client.confirmEmailChange(byID("action-token").value);
    eventAbort?.abort();
    form.hidden = true;
    logout.disabled = true;
    message("Email changed; sign in with the new address");
  } catch (error) { message(`Email confirmation failed: ${error.code ?? error.message}`); }
});
byID("upload-file").addEventListener("click", async () => {
  try {
    const file = byID("file-input").files[0], recordID = byID("file-record-id").value.trim(), field = byID("file-field").value.trim();
    if (!file || !recordID || !field) { message("Choose a file, note ID, and file field"); return; }
    await client.files("notes", recordID).upload(field, file, file.name);
    message("File uploaded");
  } catch (error) { message(`File upload failed: ${error.code ?? error.message}`); }
});
