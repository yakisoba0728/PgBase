import type { RecordData } from "./types.js";
export type AuthState = {
    token: string | null;
    record: RecordData | null;
};
/** Opt-in synchronous storage for a complete auth state, separate from token-only storage. */
export interface AuthStateStorage {
    read(): AuthState | null;
    write(state: AuthState): void;
    clear(): void;
}
/** Select browser storage deliberately; the default AuthStore remains memory-only. */
export declare function browserAuthStorage(storage: Storage, key?: string): AuthStateStorage;
/** Snapshot stored JSON data so callers cannot mutate the restored store through storage. */
export declare function authState(value: unknown): AuthState | null;
