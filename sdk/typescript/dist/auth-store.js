import { authState } from "./state-storage.js";
import { serializeAuthCookie, validateAuthCookieName } from "./auth-cookie.js";
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
        try {
            if (this.storage && "write" in this.storage)
                this.storage.write(authState({ token, record }));
            else
                this.storage?.set(token);
        }
        finally {
            this.notify();
        }
    }
    clear() {
        this.#token = null;
        this.#record = null;
        this.#generation += 1;
        try {
            this.storage?.clear();
        }
        finally {
            this.notify();
        }
    }
    /** Encodes cached state, not proof of authentication. Set secure for HTTPS. */
    exportToCookie(options = {}) {
        return serializeAuthCookie({ token: this.#token, record: this.#record }, options);
    }
    /** Loads untrusted cached state. The server must still validate its token. */
    loadFromCookie(cookie, key = "pgbase_auth") {
        validateAuthCookieName(key);
        const encoded = cookie.split(";").map((part) => part.trim()).find((part) => part.startsWith(`${key}=`))?.slice(key.length + 1);
        if (encoded === undefined)
            return;
        try {
            const state = authState(JSON.parse(decodeURIComponent(encoded)));
            if (!state)
                throw new Error("invalid auth cookie");
            this.#token = state.token;
            this.#record = state.record;
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
