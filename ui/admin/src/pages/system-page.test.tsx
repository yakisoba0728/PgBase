import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "../app";
import { activeSessionExpiresAt } from "../test/fixtures";

function response(status: number, body?: unknown): Response {
  return { ok: status >= 200 && status < 300, status, headers: new Headers({ "content-type": "application/json" }), json: vi.fn().mockResolvedValue(body) } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/admin/");
});

it("navigates to the read-only redacted system status", async () => {
  window.history.replaceState(null, "", "/admin/collections");
  const fetchMock = vi.fn()
    .mockResolvedValueOnce(response(200, {
      administrator: { id: "admin-id", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" },
      csrfToken: "csrf-token",
      expiresAt: activeSessionExpiresAt(),
    }))
    .mockResolvedValueOnce(response(200, { version: 9, collections: {} }))
    .mockResolvedValueOnce(response(200, {
      version: "v1.2.3",
      databaseReachable: true,
      migrationCurrent: 4,
      migrationLatest: 5,
      catalogVersion: 9,
      listenerReady: false,
      internalDatabaseURL: "postgres://secret",
    }));
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  render(<App />);

  await screen.findByRole("heading", { name: "Collections" });
  await user.click(screen.getByRole("button", { name: "System" }));

  expect(await screen.findByRole("heading", { name: "System" })).toBeVisible();
  expect(screen.getByText("v1.2.3")).toBeVisible();
  expect(screen.getByText("Reachable")).toBeVisible();
  expect(screen.getByText("4 / 5")).toBeVisible();
  expect(screen.getByText("9")).toBeVisible();
  expect(screen.getByText("Not ready")).toBeVisible();
  expect(screen.queryByText(/postgres:\/\/secret/)).not.toBeInTheDocument();
  expect(window.location.pathname).toBe("/admin/system");
});

it("falls back safely for malformed encoded administrator paths", async () => {
  window.history.replaceState(null, "", "/admin/collections/%E0%A4%A/records");
  vi.stubGlobal("fetch", vi.fn()
    .mockResolvedValueOnce(response(200, {
      administrator: { id: "admin-id", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" },
      csrfToken: "csrf-token",
      expiresAt: activeSessionExpiresAt(),
    }))
    .mockResolvedValueOnce(response(200, { version: 9, collections: {} })));

  render(<App />);

  expect(await screen.findByRole("heading", { name: "Collections" })).toBeVisible();
});
