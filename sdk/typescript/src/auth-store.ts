import type { RecordData } from "./types.js";
import { authState } from "./state-storage.js";
import type { AuthStateStorage } from "./state-storage.js";
import { serializeAuthCookie, validateAuthCookieName } from "./auth-cookie.js";
import type { AuthCookieOptions } from "./auth-cookie.js";
export type { AuthCookieOptions } from "./auth-cookie.js";

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

  get token(): string | null { return this.#token; }
  get record(): RecordData | null { return this.#record; }
  get isValid(): boolean { return this.#token !== null && this.#token.length > 0; }
  get generation(): number { return this.#generation; }

  save(token: string, record: RecordData | null = null): void {
    this.#token = token;
    this.#record = record;
    this.#generation += 1;
    try {
      if (this.storage && "write" in this.storage) this.storage.write(authState({ token, record })!);
      else this.storage?.set(token);
    } finally { this.notify(); }
  }

  clear(): void {
    this.#token = null;
    this.#record = null;
    this.#generation += 1;
    try { this.storage?.clear(); }
    finally { this.notify(); }
  }

  /** Encodes cached state, not proof of authentication. Set secure for HTTPS. */
  exportToCookie(options: AuthCookieOptions = {}): string {
    return serializeAuthCookie({ token: this.#token, record: this.#record }, options);
  }

  /** Loads untrusted cached state. The server must still validate its token. */
  loadFromCookie(cookie: string, key = "pgbase_auth"): void {
    validateAuthCookieName(key);
    const encoded = cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${key}=`))?.slice(key.length + 1);
    if (!encoded) return;
    try {
      const state = authState(JSON.parse(decodeURIComponent(encoded)));
      if (!state) throw new Error("invalid auth cookie");
      this.#token = state.token;
      this.#record = state.record;
      this.#generation += 1;
      this.notify();
    } catch { this.clear(); }
  }

  _onChange(listener: (generation: number) => void): () => void {
    this.#listeners.add(listener);
    return () => this.#listeners.delete(listener);
  }

  private notify(): void {
    for (const listener of this.#listeners) listener(this.#generation);
  }
}

/** Persistence remains opt-in and the caller selects its storage lifecycle. */
export function browserTokenStorage(storage: Storage, key = "pgbase.user.token"): TokenStorage {
  return {
    get: () => storage.getItem(key),
    set: (token) => storage.setItem(key, token),
    clear: () => storage.removeItem(key),
  };
}
