import type { RecordData } from "./types.js";
import type { AuthStateStorage } from "./state-storage.js";
export interface TokenStorage {
    get(): string | null;
    set(token: string): void;
    clear(): void;
}
export declare class AuthStore {
    #private;
    private readonly storage?;
    constructor(storage?: TokenStorage | AuthStateStorage | undefined);
    get token(): string | null;
    get record(): RecordData | null;
    get isValid(): boolean;
    get generation(): number;
    save(token: string, record?: RecordData | null): void;
    clear(): void;
    /** Encodes token and cached auth record for a server-rendered response cookie. */
    exportToCookie(options?: {
        key?: string;
        httpOnly?: boolean;
        secure?: boolean;
        sameSite?: "Lax" | "Strict" | "None";
        path?: string;
    }): string;
    /** Loads a state exported by [exportToCookie]; malformed cookies clear state. */
    loadFromCookie(cookie: string, key?: string): void;
    _onChange(listener: (generation: number) => void): () => void;
    private notify;
}
/**
 * Wrap browser storage only when the caller deliberately supplies it. This
 * keeps token persistence opt-in and makes the storage lifecycle visible at
 * client construction.
 */
export declare function browserTokenStorage(storage: Storage, key?: string): TokenStorage;
