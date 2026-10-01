import { PgBaseError } from "./types.js";
import type { RecordData } from "./types.js";
import { authState } from "./state-storage.js";
import type { AuthState, AuthStateStorage } from "./state-storage.js";

export interface TokenStorage {
  get(): string | null;
  set(token: string): void;
  clear(): void;
}

export class AuthStore {
  #token: string | null;
  #record: RecordData | null = null;
  #generation = 0;
  #listeners = new Set<(generation: number) => void>();

  constructor(private readonly storage?: TokenStorage | AuthStateStorage) {
    const state = storage && "read" in storage ? authState(storage.read()) : null;
    this.#token = storage && "get" in storage ? storage.get() : state?.token ?? null;
    this.#record = state?.record ?? null;
  }

  get token(): string | null {
    return this.#token;
  }
  get record(): RecordData | null { return this.#record; }
  get isValid(): boolean { return this.#token !== null && this.#token.length > 0; }

  get generation(): number {
    return this.#generation;
  }

  save(token: string, record: RecordData | null = null): void {
    this.#token = token;
    this.#record = record;
    this.#generation += 1;
    if (this.storage && "write" in this.storage) this.storage.write(authState({ token, record })!);
    else this.storage?.set(token);
    this.notify();
  }

  clear(): void {
    this.#token = null;
    this.#record = null;
    this.#generation += 1;
    this.storage?.clear();
    this.notify();
  }

  /** Encodes token and cached auth record for a server-rendered response cookie. */
  exportToCookie(options: { key?: string; httpOnly?: boolean; secure?: boolean; sameSite?: "Lax" | "Strict" | "None"; path?: string } = {}): string {
    const key = options.key ?? "pb_auth";
    const path = options.path ?? "/";
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(key) || /[;\r\n]/.test(path)) throw new PgBaseError(0, "invalid_cookie_option");
    const payload = encodeURIComponent(JSON.stringify({ token: this.#token, record: this.#record }));
    return [`${key}=${payload}`, `Path=${path}`, options.httpOnly === false ? "" : "HttpOnly", options.secure ? "Secure" : "", options.sameSite ? `SameSite=${options.sameSite}` : ""].filter(Boolean).join("; ");
  }

  /** Loads a state exported by [exportToCookie]; malformed cookies clear state. */
  loadFromCookie(cookie: string, key = "pb_auth"): void {
    const encoded = cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${key}=`))?.slice(key.length + 1);
    if (!encoded) return;
    try {
      const value = JSON.parse(decodeURIComponent(encoded)) as Partial<AuthState>;
      if ((value.token !== null && typeof value.token !== "string") || (value.record !== null && (typeof value.record !== "object" || Array.isArray(value.record)))) throw new Error("invalid auth cookie");
      this.#token = value.token ?? null;
      this.#record = (value.record ?? null) as RecordData | null;
      this.#generation += 1;
      this.notify();
    } catch { this.clear(); }
  }

  _onChange(listener: (generation: number) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  private notify(): void {
    for (const listener of this.#listeners) {
      listener(this.#generation);
    }
  }
}

/**
 * Wrap browser storage only when the caller deliberately supplies it. This
 * keeps token persistence opt-in and makes the storage lifecycle visible at
 * client construction.
 */
export function browserTokenStorage(storage: Storage, key = "pgbase.user.token"): TokenStorage {
  return {
    get: () => storage.getItem(key),
    set: (token) => storage.setItem(key, token),
    clear: () => storage.removeItem(key),
  };
}
