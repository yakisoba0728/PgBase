const output = document.querySelector("#result");
const button = document.querySelector("#run");
const exact = "900719925474099312345.0001";
const apiBase = new URLSearchParams(location.search).get("api") || "";

async function request(path, options = {}) {
  const response = await fetch(apiBase + path, options);
  const text = await response.text();
  if (!response.ok) throw new Error(`${path}: ${response.status} ${text}`);
  return { response, text, json: text ? JSON.parse(text) : null };
}

function bearer(token, body) {
  const options = {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body,
  };
  if (token) options.headers.Authorization = `Bearer ${token}`;
  return options;
}

async function nextRecordEvent(token, action) {
  const controller = new AbortController();
  const response = await fetch(apiBase + "/api/v1/user/collections/n6_tasks/events", {
    headers: { Authorization: `Bearer ${token}` },
    signal: controller.signal,
  });
  if (!response.ok || !response.body) throw new Error(`SSE: ${response.status}`);
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  await action();
  try {
    for (;;) {
      const item = await reader.read();
      if (item.done) throw new Error("SSE ended before a record event");
      buffer += decoder.decode(item.value, { stream: true });
      const frames = buffer.split("\n\n");
      buffer = frames.pop();
      for (const frame of frames) {
        if (!frame.includes("event: record")) continue;
        const line = frame.split("\n").find((value) => value.startsWith("data: "));
        if (line) return JSON.parse(line.slice(6));
      }
    }
  } finally {
    controller.abort();
  }
}

async function run() {
  button.disabled = true;
  output.textContent = "Running…";
  const suffix = Date.now();
  try {
    const email = document.querySelector("#email").value;
    const password = document.querySelector("#password").value;
    const sharedRecordId = document.querySelector("#record-id").value;
    const sharedFileId = document.querySelector("#file-id").value;
    const session = email
      ? (await request("/api/v1/auth/login", bearer("", JSON.stringify({ email, password })))).json
      : (await request("/api/v1/auth/register", bearer("", JSON.stringify({
          email: `n6-browser-${suffix}@example.test`,
          password: "correct-horse-battery-staple-N6",
        })))).json;
    const token = session.token;
    let sharedEtag = "";
    if (sharedRecordId) {
      const shared = await request(`/extensions/tasks/${sharedRecordId}`, { headers: { Authorization: `Bearer ${token}` } });
      if (!shared.text.includes(exact)) throw new Error("shared record exact JSON number changed");
      sharedEtag = shared.response.headers.get("etag");
    }
    if (sharedFileId) {
      const sharedFile = await request(`/extensions/files/${sharedFileId}/read`, bearer(token, "{}"));
      if (sharedFile.json.chunks.map(atob).join("") !== "N6 external module file payload") {
        throw new Error("shared file bytes changed");
      }
    }
    const createBody = `{"title":"browser exact","metadata":{"large":${exact}},"idempotencyKey":"browser-${suffix}"}`;
    const created = await request("/extensions/tasks", bearer(token, createBody));
    if (!created.text.includes(exact)) throw new Error("exact JSON number changed in extension response");
    const id = created.json.record.id;
    const etag = created.response.headers.get("etag");
    const fetched = await request(`/extensions/tasks/${id}`, { headers: { Authorization: `Bearer ${token}` } });
    if (!fetched.text.includes(exact) || fetched.response.headers.get("etag") !== etag) {
      throw new Error("exact number or ETag changed on extension read");
    }
    const payload = "N6 browser file payload";
    const encoded = btoa(payload);
    const uploaded = await request(`/extensions/tasks/${id}/files`, bearer(token, JSON.stringify({
      filename: "browser.txt", contentType: "text/plain", dataBase64: encoded,
    })));
    const streamed = await request(`/extensions/files/${uploaded.json.id}/read`, bearer(token, "{}"));
    if (streamed.json.chunks.map(atob).join("") !== payload) throw new Error("file bytes changed");
    const event = await nextRecordEvent(token, async () => {
      await request("/extensions/tasks", bearer(token,
        `{"title":"browser SSE","metadata":{"large":${exact}},"idempotencyKey":"browser-sse-${suffix}"}`));
    });
    const result = { status: "PASS", sharedRecordId, sharedEtag, sharedFileId, id, etag, exact, fileId: uploaded.json.id, eventId: event.eventId };
    output.textContent = JSON.stringify(result, null, 2);
    output.dataset.acceptance = "pass";
    document.title = "PASS · PgBase JavaScript extension";
  } catch (error) {
    output.textContent = `FAIL\n${error.stack || error}`;
    output.dataset.acceptance = "fail";
    document.title = "FAIL · PgBase JavaScript extension";
  } finally {
    button.disabled = false;
  }
}

button.addEventListener("click", run);
