import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };

function response(status: number, body?: unknown): Response {
  return { ok: status >= 200 && status < 300, status, headers: new Headers(), json: vi.fn().mockResolvedValue(body) } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/admin/");
});

describe("session lifecycle", () => {
  it("clears the authenticated shell when the server session expires", async () => {
    const expiresAt = new Date(Date.now() + 250).toISOString();
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(response(200, { administrator, csrfToken: "csrf-token", expiresAt }))
      .mockResolvedValueOnce(response(200, { version: 0, collections: {} })));
    render(<App />);
    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();
    expect(await screen.findByRole("heading", { name: "Sign in to pgBase" }, { timeout: 1500 })).toBeVisible();
    expect(window.location.pathname).toBe("/admin/login");
  });

  it("clears authenticated state when a protected request returns 401", async () => {
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(response(200, { administrator, csrfToken: "csrf-token", expiresAt: new Date(Date.now() + 60_000).toISOString() }))
      .mockResolvedValueOnce(response(401, { error: { code: "authentication_required", message: "Authentication required", details: {} } })));
    render(<App />);
    expect(await screen.findByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    expect(window.location.pathname).toBe("/admin/login");
  });
});
