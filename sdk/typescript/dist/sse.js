import { PgBaseError } from "./types.js";
export function canonicalDeliveryCursor(value) {
    if (typeof value === "number" && !Number.isSafeInteger(value))
        throw new PgBaseError(0, "invalid_cursor");
    const text = String(value);
    if (!/^\d+$/.test(text))
        throw new PgBaseError(0, "invalid_cursor");
    const cursor = BigInt(text);
    if (cursor > 9223372036854775807n)
        throw new PgBaseError(0, "invalid_cursor");
    return cursor.toString();
}
export async function* parseEventStream(body, signal) {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    let buffered = "";
    try {
        while (!signal?.aborted) {
            const { done, value } = await reader.read();
            buffered += decoder.decode(value, { stream: !done });
            const messages = buffered.split(/\r?\n\r?\n/);
            buffered = messages.pop() ?? "";
            for (const raw of messages) {
                const message = parseSSEMessage(raw);
                if (message) {
                    yield message;
                }
            }
            if (done) {
                break;
            }
        }
    }
    finally {
        reader.releaseLock();
    }
}
export function parseSSEMessage(raw) {
    const message = { data: "" };
    for (const line of raw.split(/\r?\n/)) {
        if (line.startsWith("event:")) {
            message.event = line.slice(6).trim();
        }
        else if (line.startsWith("id:")) {
            message.id = line.slice(3).trim();
        }
        else if (line.startsWith("data:")) {
            message.data += `${message.data ? "\n" : ""}${line.slice(5).trimStart()}`;
        }
    }
    return message.data ? message : undefined;
}
export function deliveryCursor(message) {
    if (!message.id || !/^\d+$/.test(message.id)) {
        return undefined;
    }
    try {
        const cursor = BigInt(message.id);
        return cursor <= 9223372036854775807n ? cursor.toString() : undefined;
    }
    catch {
        return undefined;
    }
}
export function recordEvent(message, deliveryId) {
    if (message.event !== "record") {
        return undefined;
    }
    try {
        const value = JSON.parse(message.data);
        if (!value.eventId || typeof value.eventId !== "string") {
            return undefined;
        }
        return { ...value, deliveryId };
    }
    catch {
        return undefined;
    }
}
export function waitForReconnect(delay, signal) {
    if (signal?.aborted)
        return Promise.resolve();
    return new Promise((resolve) => {
        const finish = () => {
            clearTimeout(timer);
            signal?.removeEventListener("abort", finish);
            resolve();
        };
        const timer = setTimeout(finish, delay);
        signal?.addEventListener("abort", finish, { once: true });
    });
}
