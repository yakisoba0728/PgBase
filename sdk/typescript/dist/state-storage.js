/** Select browser storage deliberately; the default AuthStore remains memory-only. */
export function browserAuthStorage(storage, key = "pgbase.user.auth") {
    return {
        read: () => {
            const raw = storage.getItem(key);
            if (raw === null)
                return null;
            try {
                return authState(JSON.parse(raw));
            }
            catch {
                return null;
            }
        },
        write: (state) => storage.setItem(key, JSON.stringify(state)),
        clear: () => storage.removeItem(key),
    };
}
/** Snapshot stored JSON data so callers cannot mutate the restored store through storage. */
export function authState(value) {
    if (!value || typeof value !== "object")
        return null;
    const state = value;
    if (state.token !== null && typeof state.token !== "string")
        return null;
    if (state.record !== null && (typeof state.record !== "object" || Array.isArray(state.record)))
        return null;
    if (!state.token)
        return { token: null, record: null };
    return JSON.parse(JSON.stringify({ token: state.token, record: state.record ?? null }));
}
