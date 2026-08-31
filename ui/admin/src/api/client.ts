import type { AdminStatusResponse, ErrorEnvelope, LoginInput, SessionResponse, SetupInput } from "./types";
import { parseJSON, stringifyJSON } from "./json";

export class APIClientError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    public readonly details: Record<string, string[]> = {},
    message = "Request failed",
    public readonly headers = new Headers(),
  ) {
    super(message);
    this.name = "APIClientError";
  }
}

interface RequestBehavior {
  notifyUnauthorized?: boolean;
}

export interface APIResponse<T> {
  data: T;
  headers: Headers;
  status: number;
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
    if (typeof response.text !== "function") return await response.json();
    const text = await response.text();
    return text.trim() === "" ? undefined : parseJSON(text);
  } catch {
    return undefined;
  }
}

export class APIClient {
  constructor(private readonly onUnauthorized?: () => void) {}

  async getAdminStatus(signal?: AbortSignal): Promise<AdminStatusResponse> {
    return this.request<AdminStatusResponse>("/api/admin/status", { signal }, { notifyUnauthorized: false });
  }

  async getSession(signal?: AbortSignal): Promise<SessionResponse> {
    return this.request<SessionResponse>("/api/admin/session", { signal }, { notifyUnauthorized: false });
  }

  async setup(input: SetupInput): Promise<SessionResponse> {
    return this.request<SessionResponse>("/api/admin/setup", {
      method: "POST",
      body: stringifyJSON(input),
    }, { notifyUnauthorized: false });
  }

  async login(input: LoginInput): Promise<SessionResponse> {
    return this.request<SessionResponse>("/api/admin/auth/login", {
      method: "POST",
      body: stringifyJSON(input),
    }, { notifyUnauthorized: false });
  }

  async logout(csrfToken: string): Promise<void> {
    await this.request<void>("/api/admin/auth/logout", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken, "X-PgBase-CSRF": csrfToken },
    });
  }

  async request<T>(path: string, init: RequestInit = {}, behavior: RequestBehavior = {}): Promise<T> {
    return (await this.requestWithResponse<T>(path, init, behavior)).data;
  }

  async requestWithResponse<T>(path: string, init: RequestInit = {}, behavior: RequestBehavior = {}): Promise<APIResponse<T>> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body !== undefined) headers.set("Content-Type", "application/json");

    const response = await fetch(path, {
      ...init,
      headers,
      credentials: "include",
    });
    if (response.ok) {
      const data = response.status === 204 ? undefined as T : await responseBody(response) as T;
      return { data, headers: new Headers(response.headers), status: response.status };
    }

    const body = await responseBody(response);
    if (response.status === 401 && behavior.notifyUnauthorized !== false) this.onUnauthorized?.();
    if (isErrorEnvelope(body)) {
      throw new APIClientError(response.status, body.error.code, body.error.details ?? {}, body.error.message, new Headers(response.headers));
    }
    throw new APIClientError(response.status, "request_failed", {}, "Request failed", new Headers(response.headers));
  }
}
