import { PgBaseError } from "./types.js";
import { authState } from "./state-storage.js";
export class AuthStore {
    storage;
    #token;
    #record = null;
    #generation = 0;
    #listeners = new Set();
    constructor(storage) {
        this.storage = storage;
        const state = storage && "read" in storage ? authState(storage.read()) : null;
        this.#token = storage && "get" in storage ? storage.get() : state?.token ?? null;
        this.#record = state?.record ?? null;
    }
    get token() {
        return this.#token;
    }
    get record() { return this.#record; }
    get isValid() { return this.#token !== null && this.#token.length > 0; }
    get generation() {
        return this.#generation;
    }
    save(token, record = null) {
        this.#token = token;
        this.#record = record;
        this.#generation += 1;
        if (this.storage && "write" in this.storage)
            this.storage.write(authState({ token, record }));
        else
            this.storage?.set(token);
        this.notify();
    }
    clear() {
        this.#token = null;
        this.#record = null;
        this.#generation += 1;
        this.storage?.clear();
        this.notify();
    }
    /** Encodes token and cached auth record for a server-rendered response cookie. */
    exportToCookie(options = {}) {
        const key = options.key ?? "pb_auth";
        const path = options.path ?? "/";
        if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(key) || /[;\r\n]/.test(path))
            throw new PgBaseError(0, "invalid_cookie_option");
        const payload = encodeURIComponent(JSON.stringify({ token: this.#token, record: this.#record }));
        return [`${key}=${payload}`, `Path=${path}`, options.httpOnly === false ? "" : "HttpOnly", options.secure ? "Secure" : "", options.sameSite ? `SameSite=${options.sameSite}` : ""].filter(Boolean).join("; ");
    }
    /** Loads a state exported by [exportToCookie]; malformed cookies clear state. */
    loadFromCookie(cookie, key = "pb_auth") {
        const encoded = cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${key}=`))?.slice(key.length + 1);
        if (!encoded)
            return;
        try {
            const value = JSON.parse(decodeURIComponent(encoded));
            if ((value.token !== null && typeof value.token !== "string") || (value.record !== null && (typeof value.record !== "object" || Array.isArray(value.record))))
                throw new Error("invalid auth cookie");
            this.#token = value.token ?? null;
            this.#record = (value.record ?? null);
            this.#generation += 1;
            this.notify();
        }
        catch {
            this.clear();
        }
    }
    _onChange(listener) {
        this.#listeners.add(listener);
        return () => this.#listeners.delete(listener);
    }
    notify() {
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
export function browserTokenStorage(storage, key = "pgbase.user.token") {
    return {
        get: () => storage.getItem(key),
        set: (token) => storage.setItem(key, token),
        clear: () => storage.removeItem(key),
    };
}
