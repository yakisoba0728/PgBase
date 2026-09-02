import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";
import { SessionProvider, useSession } from "./session";

const administrator = { id: "9a2e0000-0000-4000-8000-000000000001", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" };

function response(status: number, body?: unknown): Response {
  return { ok: status >= 200 && status < 300, status, headers: new Headers(), text: vi.fn().mockResolvedValue(body === undefined ? "" : JSON.stringify(body)) } as unknown as Response;
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

  it("keeps a fresh login when an old logout finishes later", async () => {
    let finishOldLogout: (response: Response) => void = () => {};
    const oldLogout = new Promise<Response>((resolve) => { finishOldLogout = resolve; });
    const session = (csrfToken: string) => response(200, {
      administrator, csrfToken, expiresAt: new Date(Date.now() + 60_000).toISOString(),
    });
    vi.stubGlobal("fetch", vi.fn()
      .mockResolvedValueOnce(session("csrf-old"))
      .mockReturnValueOnce(oldLogout)
      .mockResolvedValueOnce(session("csrf-fresh")));
    const wrapper = ({ children }: { children: ReactNode }) => <SessionProvider>{children}</SessionProvider>;
    const { result } = renderHook(() => useSession(), { wrapper });
    await waitFor(() => expect(result.current.state.kind).toBe("authenticated"));

    let staleLogout: Promise<void> = Promise.resolve();
    act(() => { staleLogout = result.current.logout(); });
    await act(async () => result.current.login({ email: administrator.email, password: "fresh password" }));
    await act(async () => { finishOldLogout(response(204)); await staleLogout; });

    expect(result.current.state.kind).toBe("authenticated");
    if (result.current.state.kind === "authenticated") expect(result.current.state.csrfToken).toBe("csrf-fresh");
  });
});
