import { parseJSON } from "./json";

export interface Wire {
  status: number;
  code: string;
  details: Record<string, string[]>;
}

export class WireError extends Error {
  constructor(readonly wire: Wire) {
    super(wire.code);
    this.name = "WireError";
  }
}

export interface Request {
  path: string;
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  csrf?: boolean;
  anonymous?: boolean;
}

export interface Reply {
  data: unknown;
  headers: Headers;
  status: number;
}

export function envelope(body: unknown): Omit<Wire, "status"> | null {
  if (typeof body !== "object" || body === null || !("error" in body))
    return null;
  const error = body.error as {
    code?: unknown;
    message?: unknown;
    details?: Record<string, string[]>;
  };
  if (typeof error.code !== "string" || typeof error.message !== "string")
    return null;
  return { code: error.code, details: error.details ?? {} };
}

/** All JSON responses use the lossless parser; port codecs never call response.json(). */
export async function decode(response: Response): Promise<unknown> {
  const text = await response.text();
  return text.trim() === "" ? undefined : parseJSON(text);
}
