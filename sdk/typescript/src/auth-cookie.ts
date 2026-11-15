import { PgBaseError } from "./types.js";

export interface AuthCookieOptions {
  key?: string;
  httpOnly?: boolean;
  secure?: boolean;
  sameSite?: "Lax" | "Strict" | "None";
  path?: string;
}

export function validateAuthCookieName(key: string): void {
  // Reject every non-token character, including a trailing newline (which a
  // permissive end-of-line regex anchor can otherwise overlook).
  if (typeof key !== "string" || key.length === 0 || /[^!#$%&'*+.^_`|~0-9A-Za-z-]/.test(key)) {
    throw new PgBaseError(0, "invalid_cookie_option");
  }
}

export function serializeAuthCookie(state: unknown, options: AuthCookieOptions): string {
  const key = options.key ?? "pgbase_auth";
  const path = options.path ?? "/";
  const httpOnly = options.httpOnly ?? true;
  const secure = options.secure ?? false;
  const sameSite = options.sameSite ?? "Lax";
  validateAuthCookieName(key);
  if (typeof path !== "string" || !path.startsWith("/") || /[^\x20-\x3a\x3c-\x7e]/.test(path) ||
      typeof httpOnly !== "boolean" || typeof secure !== "boolean" ||
      !["Lax", "Strict", "None"].includes(sameSite) || (sameSite === "None" && !secure)) {
    throw new PgBaseError(0, "invalid_cookie_option");
  }
  const prefix = key.toLowerCase();
  const host = prefix.startsWith("__host-");
  const http = prefix.startsWith("__http-") || prefix.startsWith("__host-http-");
  if (((host || http || prefix.startsWith("__secure-")) && !secure) || (host && path !== "/") || (http && !httpOnly)) {
    throw new PgBaseError(0, "invalid_cookie_option");
  }
  const payload = encodeURIComponent(JSON.stringify(state));
  return [`${key}=${payload}`, `Path=${path}`, httpOnly ? "HttpOnly" : "", secure ? "Secure" : "", `SameSite=${sameSite}`].filter(Boolean).join("; ");
}
