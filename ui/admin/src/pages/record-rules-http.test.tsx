import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { httpBackend } from "../backend/http-backend";
import { activeSessionExpiresAt, administrator, sessionWrapper } from "../test/harness";
import { wireResponse } from "../test/http-harness";
import { memoryHistory } from "../test/memory-history";
import { RecordRulesEditor } from "./record-rules-editor";

const ownerRules = { list: { op: "owner" }, view: { op: "owner" }, create: { op: "owner" }, update: { op: "owner" }, delete: { op: "owner" } };
const recordID = "0199ab10-0000-7000-8000-000000000111";

function renderHTTPRules(response: unknown, rulesJSON = JSON.stringify(ownerRules)) {
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    if (path === "/api/admin/collections/notes/rules" && (!init?.method || init.method === "GET")) {
      return wireResponse({ status: 200, body: `{"collection":"notes","rules":${rulesJSON},"revision":4}` });
    }
    const body = path === "/api/admin/session"
      ? { administrator, expiresAt: activeSessionExpiresAt(), csrfToken: "rules-csrf" }
      : init?.method === "PUT" || init?.method === "POST"
        ? response
        : { collection: "notes", rules: ownerRules, revision: 4 };
    return wireResponse({ status: 200, body: JSON.stringify(body) });
  });
  vi.stubGlobal("fetch", fetch);
  render(<RecordRulesEditor collection="notes" />, { wrapper: sessionWrapper(httpBackend(), memoryHistory()) });
  return fetch;
}

afterEach(() => vi.unstubAllGlobals());

it("saves rules through the native HTTP contract without an unsupported revision member", async () => {
  const savedRules = { ...ownerRules, list: { op: "allow" } };
  const fetch = renderHTTPRules({ collection: "notes", rules: savedRules, revision: 5 });
  fireEvent.change(await screen.findByLabelText("list", { exact: true }), { target: { value: "allow" } });
  await waitFor(() => expect(screen.getByRole("button", { name: "Save rules" })).toBeEnabled());
  fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
  await waitFor(() => expect(fetch.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
  const [path, init] = fetch.mock.calls.find(([, init]) => init?.method === "PUT")!;
  expect(path).toBe("/api/admin/collections/notes/rules");
  expect(JSON.parse(init!.body as string)).toEqual({ rules: savedRules });
  expect(new Headers(init!.headers).get("X-PgBase-CSRF")).toBe("rules-csrf");
  expect(init!.credentials).toBe("include");
});

it.each(["view", "delete"] as const)("previews only the unsaved %s action over HTTP", async (action) => {
  const fetch = renderHTTPRules({ items: [{ recordId: recordID, allowed: false }] });
  const draft = { ...ownerRules, view: { op: "deny" }, delete: { op: "authenticated" } };
  const input = await screen.findByLabelText("Advanced rules JSON");
  await waitFor(() => expect(input).toBeEnabled());
  fireEvent.change(input, { target: { value: JSON.stringify(draft) } });
  fireEvent.change(screen.getByLabelText("Action", { exact: true }), { target: { value: action } });
  fireEvent.change(screen.getByLabelText("Record IDs (comma or space separated)"), { target: { value: recordID } });
  fireEvent.click(screen.getByRole("button", { name: "Preview access" }));
  expect(await screen.findByText(`${recordID}: denied`)).toBeVisible();
  const [path, init] = fetch.mock.calls.find(([, init]) => init?.method === "POST")!;
  expect(path).toBe("/api/admin/collections/notes/rules/preview");
  expect(JSON.parse(init!.body as string)).toEqual({ rule: draft[action], action, recordIds: [recordID] });
  expect(new Headers(init!.headers).get("X-PgBase-CSRF")).toBe("rules-csrf");
});

it("keeps advanced edits when changing another preset and resets to the last loaded policy", async () => {
  renderHTTPRules({});
  const input = await screen.findByLabelText("Advanced rules JSON");
  await waitFor(() => expect(input).toBeEnabled());
  fireEvent.change(input, { target: { value: JSON.stringify({ ...ownerRules, view: { op: "deny" } }) } });
  fireEvent.change(screen.getByLabelText("list", { exact: true }), { target: { value: "allow" } });
  expect(JSON.parse((input as HTMLTextAreaElement).value)).toEqual({ ...ownerRules, view: { op: "deny" }, list: { op: "allow" } });
  expect(screen.getByLabelText("view", { exact: true })).toHaveValue("deny");
  fireEvent.click(screen.getByRole("button", { name: /^Reset$/ }));
  expect(JSON.parse((input as HTMLTextAreaElement).value)).toEqual(ownerRules);
});

it("preserves exact numeric rule literals from HTTP load through preset editing, preview and save", async () => {
  const raw = JSON.stringify(ownerRules).replace('"view":{"op":"owner"}', '"view":{"op":"eq","field":"amount","value":9007199254740993}');
  const fetch = renderHTTPRules({ items: [{ recordId: recordID, allowed: true }], rules: ownerRules }, raw);
  const input = await screen.findByLabelText("Advanced rules JSON");
  await waitFor(() => expect(input).toBeEnabled());
  expect((input as HTMLTextAreaElement).value).toContain('"value": 9007199254740993');
  fireEvent.change(screen.getByLabelText("list", { exact: true }), { target: { value: "allow" } });
  fireEvent.change(screen.getByLabelText("Record IDs (comma or space separated)"), { target: { value: recordID } });
  fireEvent.click(screen.getByRole("button", { name: "Preview access" }));
  expect(await screen.findByText(`${recordID}: allowed`)).toBeVisible();
  expect(fetch.mock.calls.find(([, init]) => init?.method === "POST")![1]!.body).toContain('"value":9007199254740993');
  fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
  await waitFor(() => expect(fetch.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
  expect(fetch.mock.calls.find(([, init]) => init?.method === "PUT")![1]!.body).toContain('"value":9007199254740993');
});

it("removes a preview verdict as soon as the draft or evaluation subject changes", async () => {
  renderHTTPRules({ items: [{ recordId: recordID, allowed: true }] });
  await screen.findByLabelText("Advanced rules JSON");
  await waitFor(() => expect(screen.getByRole("button", { name: "Preview access" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("Record IDs (comma or space separated)"), { target: { value: recordID } });
  fireEvent.click(screen.getByRole("button", { name: "Preview access" }));
  expect(await screen.findByText(`${recordID}: allowed`)).toBeVisible();
  fireEvent.change(screen.getByLabelText("view", { exact: true }), { target: { value: "deny" } });
  expect(screen.queryByLabelText("Rule preview results")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Preview access" }));
  expect(await screen.findByText(`${recordID}: allowed`)).toBeVisible();
  fireEvent.change(screen.getByLabelText("User ID (optional for anonymous)"), { target: { value: administrator.id } });
  expect(screen.queryByLabelText("Rule preview results")).not.toBeInTheDocument();
});
