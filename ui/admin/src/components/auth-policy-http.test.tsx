import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { httpBackend } from "../backend/http-backend";
import { activeSessionExpiresAt, administrator, sessionWrapper } from "../test/harness";
import { wireResponse } from "../test/http-harness";
import { memoryHistory } from "../test/memory-history";
import { AuthAdministrationPanel } from "./auth-administration-panel";

const policy = { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 72, passwordPattern: "", identityFields: ["email"], mfaEnabled: true, mfaDurationSeconds: 1800, mfaRule: "", otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5 };

function renderPolicy() {
  const fetch = vi.fn(async (path: string, init?: RequestInit) => wireResponse({ status: 200, body: JSON.stringify(
    path === "/api/admin/session" ? { administrator, expiresAt: activeSessionExpiresAt(), csrfToken: "policy-csrf" }
      : init?.method === "PUT" ? JSON.parse(init.body as string) : { policy, providers: [], templates: [] },
  ) }));
  vi.stubGlobal("fetch", fetch);
  render(<AuthAdministrationPanel collectionId="realm-id" />, { wrapper: sessionWrapper(httpBackend(), memoryHistory()) });
  return fetch;
}

afterEach(() => vi.unstubAllGlobals());

it("loads and updates every native policy field through the authenticated HTTP adapter", async () => {
  const fetch = renderPolicy();
  fireEvent.click(await screen.findByText("Authentication settings"));
  expect(await screen.findByLabelText("Maximum password bytes")).toHaveValue(72);
  await waitFor(() => expect(screen.getByRole("button", { name: "Save policy" })).toBeEnabled());
  fireEvent.change(screen.getByLabelText("Maximum password bytes"), { target: { value: "128" } });
  fireEvent.change(screen.getByLabelText("Password pattern"), { target: { value: "[A-Z]" } });
  fireEvent.change(screen.getByLabelText("Login identity fields"), { target: { value: "email\nusername" } });
  fireEvent.change(screen.getByLabelText("MFA challenge lifetime in seconds"), { target: { value: "120" } });
  fireEvent.change(screen.getByLabelText("MFA rule JSON"), { target: { value: '{"field":"verified","equals":true}' } });
  fireEvent.click(screen.getByRole("button", { name: "Save policy" }));
  await screen.findByText(/^Policy saved\./);
  const [path, init] = fetch.mock.calls.find(([, request]) => request?.method === "PUT")!;
  expect(path).toBe("/api/admin/auth-collections/realm-id/policy");
  expect(JSON.parse(init!.body as string)).toEqual({ ...policy, maximumPasswordBytes: 128, passwordPattern: "[A-Z]", identityFields: ["email", "username"], mfaDurationSeconds: 120, mfaRule: '{"field":"verified","equals":true}' });
  expect(new Headers(init!.headers).get("X-PgBase-CSRF")).toBe("policy-csrf");
  expect(init!.credentials).toBe("include");
});

it("keeps invalid password bounds out of policy mutations", async () => {
  const fetch = renderPolicy();
  fireEvent.click(await screen.findByText("Authentication settings"));
  const maximum = await screen.findByLabelText("Maximum password bytes");
  fireEvent.change(maximum, { target: { value: "10" } });
  fireEvent.click(screen.getByRole("button", { name: "Save policy" }));
  expect((maximum as HTMLInputElement).checkValidity()).toBe(false);
  expect(fetch.mock.calls.some(([, request]) => request?.method === "PUT")).toBe(false);
});
