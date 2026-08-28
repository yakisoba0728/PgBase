import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "../app";
import { APIClient, APIClientError } from "../api/client";

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

function setupRequired(): Response {
  return response(404, {
    error: { code: "setup_required", message: "Administrator setup is required", details: {} },
  });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("APIClient", () => {
  it("sends credential-included JSON requests and decodes field errors without retrying a POST", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(400, {
      error: {
        code: "validation_failed",
        message: "Authentication input is invalid",
        details: { email: ["invalid"] },
      },
    }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(new APIClient().setup({
      setupToken: "first-run-token",
      email: "admin@example.com",
      password: "a secure password",
    })).rejects.toMatchObject({
      status: 400,
      code: "validation_failed",
      details: { email: ["invalid"] },
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [path, options] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/admin/setup");
    expect(options.method).toBe("POST");
    expect(options.credentials).toBe("include");
    expect(new Headers(options.headers).get("Content-Type")).toBe("application/json");
  });

  it("adds the CSRF header on logout and passes abort signals to session fetches", async () => {
    const fetchMock = vi.fn().mockResolvedValue(response(204));
    vi.stubGlobal("fetch", fetchMock);
    const signal = new AbortController().signal;
    const client = new APIClient();

    await client.logout("csrf-token");
    await client.getSession(signal);

    const [logoutPath, logoutOptions] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(logoutPath).toBe("/api/admin/auth/logout");
    expect(logoutOptions.method).toBe("POST");
    expect(logoutOptions.credentials).toBe("include");
    expect(new Headers(logoutOptions.headers).get("X-CSRF-Token")).toBe("csrf-token");
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/admin/session", expect.objectContaining({ signal }));
  });
});

describe("administrator setup", () => {
	it("reaches first-run setup through the unauthenticated backend status contract", async () => {
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(response(401, {
				error: { code: "authentication_required", message: "Administrator authentication is required", details: {} },
			}))
			.mockResolvedValueOnce(response(200, { setupRequired: true }));
		vi.stubGlobal("fetch", fetchMock);
		render(<App />);

		expect(await screen.findByRole("heading", { name: "Set up pgBase" })).toBeVisible();
		expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/admin/session", expect.anything());
		expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/admin/status", expect.anything());
		expect(window.location.pathname).toBe("/admin/setup");
	});

	it("conceals the setup token unless the administrator explicitly reveals it", async () => {
		vi.stubGlobal("fetch", vi.fn().mockResolvedValue(setupRequired()));
		const user = userEvent.setup();
		render(<App />);

		const token = await screen.findByLabelText("Setup token");
		expect(token).toHaveAttribute("type", "password");
		await user.click(screen.getByRole("button", { name: "Show setup token" }));
		expect(token).toHaveAttribute("type", "text");
		await user.click(screen.getByRole("button", { name: "Hide setup token" }));
		expect(token).toHaveAttribute("type", "password");
	});

  it("validates required values and matching passwords before calling the server", async () => {
    const fetchMock = vi.fn().mockResolvedValue(setupRequired());
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    await user.click(screen.getByRole("button", { name: "Create administrator" }));

    expect(await screen.findByText("Setup token is required.")).toBeVisible();
    expect(screen.getByText("Email is required.")).toBeVisible();
    expect(screen.getByText("Password is required.")).toBeVisible();
    expect(screen.getByLabelText("Setup token")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Setup token")).toHaveAttribute("aria-describedby", "setup-token-error");
    expect(screen.getByLabelText("Setup token")).toHaveFocus();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await user.type(screen.getByLabelText("Setup token"), "first-run-token");
    await user.type(screen.getByLabelText("Email"), "admin@example.com");
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "different password");
    await user.click(screen.getByRole("button", { name: "Create administrator" }));

    expect(await screen.findByText("Passwords do not match.")).toBeVisible();
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByLabelText("Confirm password")).toHaveAttribute("aria-describedby", "confirmation-error");
    expect(screen.getByLabelText("Confirm password")).toHaveFocus();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("allowlists server field errors, clears password fields, and focuses the first invalid field", async () => {
    let resolveSetup: ((value: Response) => void) | undefined;
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(setupRequired())
      .mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveSetup = resolve; }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    await user.type(screen.getByLabelText("Setup token"), "first-run-token");
    await user.type(screen.getByLabelText("Email"), "admin@example.com");
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Create administrator" }));

    expect(screen.getByRole("button", { name: "Creating administrator…" })).toBeDisabled();
    resolveSetup?.(response(400, {
      error: {
        code: "validation_failed",
        message: "unsafe internal detail",
        details: { setupToken: ["invalid"], email: ["invalid"], password: ["too_short"], unknown: ["raw detail"] },
      },
    }));

    expect(await screen.findByText("Setup token is invalid.")).toBeVisible();
    expect(await screen.findByText("Enter a valid email address.")).toBeVisible();
    expect(screen.getByText("Password does not meet requirements.")).toBeVisible();
    expect(screen.queryByText("unsafe internal detail")).not.toBeInTheDocument();
    expect(screen.queryByText("raw detail")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Setup token")).toHaveAttribute("aria-describedby", "setup-token-error");
    expect(screen.getByLabelText("Setup token")).toHaveFocus();
    expect(screen.getByLabelText("Password")).toHaveValue("");
    expect(screen.getByLabelText("Confirm password")).toHaveValue("");
  });

	it("moves to the authenticated shell after a successful setup", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(setupRequired())
      .mockResolvedValueOnce(response(201, { administrator, csrfToken: "csrf-token", expiresAt: "2026-08-29T00:00:00Z" }));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    render(<App />);

    await screen.findByRole("heading", { name: "Set up pgBase" });
    await user.type(screen.getByLabelText("Setup token"), "first-run-token");
    await user.type(screen.getByLabelText("Email"), administrator.email);
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.type(screen.getByLabelText("Confirm password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Create administrator" }));

    expect(await screen.findByRole("heading", { name: "pgBase administration" })).toBeVisible();
    expect(screen.getByText(administrator.email)).toBeVisible();
	});

	it("disables repeated logout and shows a safe failure without dropping the session", async () => {
		let resolveLogout: ((value: Response) => void) | undefined;
		const fetchMock = vi.fn()
			.mockResolvedValueOnce(response(200, { administrator, csrfToken: "csrf-token", expiresAt: "2026-08-29T00:00:00Z" }))
			.mockResolvedValueOnce(response(200, { version: 0, collections: {} }))
			.mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveLogout = resolve; }));
		vi.stubGlobal("fetch", fetchMock);
		const user = userEvent.setup();
		render(<App />);

		await screen.findByRole("heading", { name: "pgBase administration" });
		await user.click(screen.getByRole("button", { name: "Sign out" }));
		const pendingButton = screen.getByRole("button", { name: "Signing out…" });
		expect(pendingButton).toBeDisabled();
		await user.click(pendingButton);
		expect(fetchMock).toHaveBeenCalledTimes(3);
		resolveLogout?.(response(500, { error: { code: "internal_error", message: "unsafe detail", details: {} } }));

		expect(await screen.findByRole("alert")).toHaveTextContent("Unable to sign out. Try again.");
		expect(screen.queryByText("unsafe detail")).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Sign out" })).toBeEnabled();
		expect(screen.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
	});
});
