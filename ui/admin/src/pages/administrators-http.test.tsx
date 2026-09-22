import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "../app";
import { httpBackend } from "../backend/http-backend";
import { activeSessionExpiresAt, administrator } from "../test/harness";
import { wireResponse } from "../test/http-harness";
import { memoryHistory } from "../test/memory-history";

const current = { ...administrator, active: true, updatedAt: administrator.createdAt };
const other = { ...current, id: "9a2e0000-0000-4000-8000-000000000002", email: "other@example.test" };
const password = "A private password 123!";

function renderAccounts(conflict = false) {
  const fetch = vi.fn(async (path: string, init?: RequestInit) => {
    const data = init?.body ? JSON.parse(init.body as string) : {};
    if (path === "/api/admin/session") return wireResponse({ status: 200, body: JSON.stringify({ administrator, expiresAt: activeSessionExpiresAt(), csrfToken: "accounts-csrf" }) });
    if (path.startsWith("/api/admin/administrators")) {
      if (conflict && init?.method === "POST") return wireResponse({ status: 409, body: JSON.stringify({ error: { code: "administrator_conflict", message: "Conflict" } }) });
      if (path.endsWith("/revoke-sessions") || path.endsWith("/password-reset")) return wireResponse({ status: 204, body: "" });
      if (init?.method === "PATCH") return wireResponse({ status: 200, body: JSON.stringify({ ...other, active: data.active }) });
      if (init?.method === "POST") return wireResponse({ status: 201, body: JSON.stringify({ ...other, email: data.email }) });
      return wireResponse({ status: 200, body: JSON.stringify(path.includes("after=") ? { items: [other] } : { items: [current, other], nextAfter: other.id }) });
    }
    return wireResponse({ status: 404, body: "{}" });
  });
  vi.stubGlobal("fetch", fetch);
  vi.spyOn(window, "confirm").mockReturnValue(true);
  const history = memoryHistory("/admin/administrators");
  render(<App backend={httpBackend()} history={history} />);
  return { fetch, history };
}

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

it("creates an administrator through the native endpoint and clears the supplied password", async () => {
  const { fetch } = renderAccounts();
  await screen.findByRole("heading", { name: "Administrators" });
  const form = screen.getByRole("form", { name: "Create administrator" });
  await waitFor(() => expect(within(form).getByRole("button", { name: "Create administrator" })).toBeEnabled());
  fireEvent.change(within(form).getByLabelText("Email"), { target: { value: "new@example.test" } });
  fireEvent.change(within(form).getByLabelText("Password"), { target: { value: password } });
  fireEvent.click(within(form).getByRole("button", { name: "Create administrator" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Administrator created");
  const [path, init] = fetch.mock.calls.find(([, request]) => request?.method === "POST")!;
  expect(path).toBe("/api/admin/administrators");
  expect(JSON.parse(init!.body as string)).toEqual({ email: "new@example.test", password });
  expect(new Headers(init!.headers).get("X-PgBase-CSRF")).toBe("accounts-csrf");
  expect(init!.credentials).toBe("include");
  expect(within(form).getByLabelText("Password")).toHaveValue("");
});

it("loads bounded pages and protects self-deactivation while changing another administrator", async () => {
  const { fetch } = renderAccounts();
  const self = await screen.findByRole("row", { name: new RegExp(administrator.email) });
  expect(within(self).getByRole("button", { name: "Deactivate" })).toBeDisabled();
  const row = screen.getByRole("row", { name: new RegExp(other.email) });
  fireEvent.click(within(row).getByRole("button", { name: "Deactivate" }));
  expect(await screen.findByRole("status")).toHaveTextContent("inactive");
  const [path, init] = fetch.mock.calls.find(([, request]) => request?.method === "PATCH")!;
  expect(path).toBe(`/api/admin/administrators/${other.id}`);
  expect(JSON.parse(init!.body as string)).toEqual({ active: false });
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await waitFor(() => expect(fetch.mock.calls.some(([path]) => path === `/api/admin/administrators?limit=50&after=${other.id}`)).toBe(true));
});

it("sets a replacement password directly, clears it, and keeps the current administrator signed in", async () => {
  const { fetch } = renderAccounts();
  const row = await screen.findByRole("row", { name: new RegExp(other.email) });
  fireEvent.click(within(row).getByRole("button", { name: "Set password" }));
  fireEvent.change(screen.getByLabelText("New password"), { target: { value: password } });
  fireEvent.click(screen.getByRole("button", { name: "Save password" }));
  expect(await screen.findByRole("status")).toHaveTextContent("Password replaced");
  const [path, init] = fetch.mock.calls.find(([, request]) => request?.method === "POST")!;
  expect(path).toBe(`/api/admin/administrators/${other.id}/password-reset`);
  expect(JSON.parse(init!.body as string)).toEqual({ newPassword: password });
  expect(screen.queryByLabelText("New password")).not.toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "Administrators" })).toBeVisible();
});

it.each(["revoke", "password"])("ends the local session immediately after successful self %s", async (action) => {
  const { fetch, history } = renderAccounts();
  const row = await screen.findByRole("row", { name: new RegExp(administrator.email) });
  if (action === "revoke") fireEvent.click(within(row).getByRole("button", { name: "Revoke sessions" }));
  else {
    fireEvent.click(within(row).getByRole("button", { name: "Set password" }));
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: password } });
    fireEvent.click(screen.getByRole("button", { name: "Save password" }));
  }
  expect(await screen.findByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
  expect(history.path()).toBe("/admin/login");
  expect(fetch.mock.calls.some(([path]) => path.endsWith("/auth/logout"))).toBe(false);
});

it("reports a rejected account creation without retaining its password", async () => {
  renderAccounts(true);
  await screen.findByRole("heading", { name: "Administrators" });
  const form = screen.getByRole("form", { name: "Create administrator" });
  await waitFor(() => expect(within(form).getByRole("button", { name: "Create administrator" })).toBeEnabled());
  fireEvent.change(within(form).getByLabelText("Email"), { target: { value: other.email } });
  fireEvent.change(within(form).getByLabelText("Password"), { target: { value: password } });
  fireEvent.click(within(form).getByRole("button", { name: "Create administrator" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("already exists");
  expect(within(form).getByLabelText("Password")).toHaveValue("");
});

it("preserves the local session when revoking its sessions is rejected", async () => {
  const { history } = renderAccounts(true);
  const row = await screen.findByRole("row", { name: new RegExp(administrator.email) });
  fireEvent.click(within(row).getByRole("button", { name: "Revoke sessions" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("Refresh and try again");
  expect(screen.getByRole("heading", { name: "Administrators" })).toBeVisible();
  expect(history.path()).toBe("/admin/administrators");
});

it("does not expire a replacement session when an earlier own-account revoke finishes late", async () => {
  let release!: () => void;
  const held = new Promise<void>((resolve) => { release = resolve; });
  vi.stubGlobal("fetch", vi.fn(async (path: string) => {
    if (path.endsWith("/revoke-sessions")) { await held; return wireResponse({ status: 204, body: "" }); }
    return wireResponse({ status: 200, body: JSON.stringify({ administrator: path.endsWith("/login") ? other : administrator, expiresAt: activeSessionExpiresAt(), csrfToken: "csrf" }) });
  }));
  const backend = httpBackend();
  const expired = vi.fn();
  backend.session.onExpired(expired);
  await backend.session.resume();
  const pending = backend.administrators.revokeSessions(administrator.id);
  await backend.session.login({ email: other.email, password });
  release(); await pending;
  expect(expired).not.toHaveBeenCalled();
});
