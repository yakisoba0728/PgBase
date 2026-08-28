import type { ErrorEnvelope, LoginInput, SessionResponse, SetupInput } from "./types";

export class APIClientError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    public readonly details: Record<string, string[]> = {},
    message = "Request failed",
  ) {
    super(message);
    this.name = "APIClientError";
  }
}

function isErrorEnvelope(value: unknown): value is ErrorEnvelope {
  if (typeof value !== "object" || value === null || !("error" in value)) return false;
  const error = value.error;
  return typeof error === "object" && error !== null &&
    typeof (error as { code?: unknown }).code === "string" &&
    typeof (error as { message?: unknown }).message === "string";
}

async function responseBody(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return undefined;
  }
}

export class APIClient {
  async getSession(signal?: AbortSignal): Promise<SessionResponse> {
    return this.request<SessionResponse>("/api/admin/session", { signal });
  }

  async setup(input: SetupInput): Promise<SessionResponse> {
    return this.request<SessionResponse>("/api/admin/setup", {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  async login(input: LoginInput): Promise<SessionResponse> {
    return this.request<SessionResponse>("/api/admin/auth/login", {
      method: "POST",
      body: JSON.stringify(input),
    });
  }

  async logout(csrfToken: string): Promise<void> {
    await this.request<void>("/api/admin/auth/logout", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken, "X-PgBase-CSRF": csrfToken },
    });
  }

  async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body !== undefined) headers.set("Content-Type", "application/json");

    const response = await fetch(path, {
      ...init,
      headers,
      credentials: "include",
    });
    if (response.ok) {
      if (response.status === 204) return undefined as T;
      return await response.json() as T;
    }

    const body = await responseBody(response);
    if (isErrorEnvelope(body)) {
      throw new APIClientError(response.status, body.error.code, body.error.details ?? {}, body.error.message);
    }
    throw new APIClientError(response.status, "request_failed");
  }
}
