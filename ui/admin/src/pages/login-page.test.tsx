import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";

const administrator = {
  id: "9a2e0000-0000-4000-8000-000000000001",
  email: "admin@example.com",
  createdAt: "2026-08-28T00:00:00Z",
};

function response(status: number, body?: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "content-type": "application/json" }),
    json: vi.fn().mockResolvedValue(body),
  } as unknown as Response;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("administrator login", () => {
  it("shows validation errors and does not submit incomplete credentials", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(401, {
      error: { code: "authentication_required", message: "Administrator authentication is required", details: {} },
    }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Email is required.")).toBeVisible();
    expect(screen.getByText("Password is required.")).toBeVisible();
    expect(screen.getByLabelText("Email")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Email")).toHaveAttribute("aria-describedby", "email-error");
    expect(screen.getByLabelText("Email")).toHaveFocus();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("shows safe credential errors, clears the password, and disables the pending form", async () => {
    let resolveLogin: ((value: Response) => void) | undefined;
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(401, {
        error: { code: "authentication_required", message: "Administrator authentication is required", details: {} },
      }))
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveLogin = resolve; }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "bad password");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(screen.getByRole("button", { name: "Signing in…" })).toBeDisabled();
    resolveLogin?.(response(401, {
      error: { code: "invalid_credentials", message: "unsafe internal detail", details: {} },
    }));

    expect(await screen.findByText("Email or password is incorrect.")).toBeVisible();
    expect(screen.queryByText("unsafe internal detail")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByRole("alert")).toHaveFocus();
  });

  it("moves to the authenticated shell after a successful login", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(401, {
        error: { code: "authentication_required", message: "Administrator authentication is required", details: {} },
      }))
      .mockResolvedValueOnce(response(200, { administrator, csrfToken: "csrf-token", expiresAt: "2026-08-29T00:00:00Z" }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Sign in to pgBase" });
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();
    expect(screen.getByText(administrator.email)).toBeVisible();
  });
});
