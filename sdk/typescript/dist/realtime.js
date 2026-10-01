import { PgBaseError } from "./types.js";
import { parseEventStream, canonicalDeliveryCursor, waitForReconnect } from "./sse.js";
/** Multiplexes realtime topics over one durable SSE connection. */
export class RealtimeService {
    client;
    listeners = new Map();
    controller;
    clientId;
    lastEventId;
    running;
    ready;
    resolveReady;
    rejectReady;
    readySettled = false;
    maxReconnects = 5;
    reconnectDelayMs = 250;
    observers = new Set();
    currentStatus = Object.freeze({ state: "stopped", attempt: 0 });
    constructor(client) {
        this.client = client;
    }
    get status() { return Object.freeze({ ...this.currentStatus, lastEventId: this.lastEventId }); }
    /** Observes the shared session, starting with its current snapshot. */
    onStatus(observer) {
        this.observers.add(observer);
        this.invokeObserver(observer, this.status);
        return () => { this.observers.delete(observer); };
    }
    /** Stops all subscriptions and forgets the replay cursor for an explicit resync. */
    async resetCursor() {
        await this.unsubscribe();
        await this.running;
        this.lastEventId = undefined;
        this.notify({ state: "stopped", attempt: 0, reason: "reset" });
    }
    async subscribe(topic, listener, options = {}) {
        if (!validRealtimeTopic(topic))
            throw new PgBaseError(0, "invalid_realtime_topic");
        if (options.maxReconnects !== undefined && (!Number.isInteger(options.maxReconnects) || options.maxReconnects < 0))
            throw new PgBaseError(0, "invalid_reconnect_options");
        if (options.reconnectDelayMs !== undefined && (!Number.isFinite(options.reconnectDelayMs) || options.reconnectDelayMs < 0))
            throw new PgBaseError(0, "invalid_reconnect_options");
        if (this.controller?.signal.aborted && this.running)
            await this.running;
        let set = this.listeners.get(topic);
        if (!set) {
            set = new Set();
            this.listeners.set(topic, set);
        }
        set.add(listener);
        this.ensureRunning(options);
        if (this.clientId)
            await this.sync();
        else
            await this.ready;
        return async () => this.unsubscribe(topic, listener);
    }
    /**
     * Subscribes to an application-owned topic without exposing its reserved
     * realtime wire name. The callback receives the original UTF-8 topic.
     */
    async subscribeApplication(topic, listener) {
        const wireTopic = applicationWireTopic(topic);
        return this.subscribe(wireTopic, (event) => this.deliver(listener, { topic, data: event.data }, topic));
    }
    async unsubscribe(topic, listener) {
        if (topic === undefined)
            this.listeners.clear();
        else if (listener === undefined)
            this.listeners.delete(topic);
        else {
            const set = this.listeners.get(topic);
            set?.delete(listener);
            if (set?.size === 0)
                this.listeners.delete(topic);
        }
        if (this.listeners.size === 0) {
            this.controller?.abort();
            this.clientId = undefined;
            this.fail(new PgBaseError(0, "realtime_unsubscribed"), "unsubscribed");
            return;
        }
        if (this.clientId)
            await this.sync();
    }
    ensureRunning(options) {
        if (this.running)
            return;
        this.maxReconnects = options.maxReconnects ?? 5;
        this.reconnectDelayMs = options.reconnectDelayMs ?? 250;
        this.readySettled = false;
        this.ready = new Promise((resolve, reject) => { this.resolveReady = resolve; this.rejectReady = reject; });
        const controller = this.controller = new AbortController();
        this.running = Promise.resolve().then(() => this.run(controller)).finally(() => { this.running = undefined; this.ready = undefined; this.resolveReady = undefined; this.rejectReady = undefined; });
    }
    async sync() { if (this.clientId)
        await this.client._request("/api/realtime", { method: "POST", body: JSON.stringify({ clientId: this.clientId, subscriptions: [...this.listeners.keys()] }) }, this.controller?.signal); }
    async run(controller) {
        if (this.listeners.size === 0)
            return;
        let reconnects = 0;
        this.notify({ state: "connecting", attempt: 0 });
        while (!controller.signal.aborted && this.listeners.size) {
            let reconnectError;
            try {
                const response = await this.client._eventStream("/api/realtime", this.lastEventId, controller.signal);
                for await (const message of parseEventStream(response.body, controller.signal)) {
                    const data = JSON.parse(message.data);
                    if (message.event === "PB_ERROR") {
                        const code = typeof data === "object" && data !== null && "code" in data && typeof data.code === "string" ? data.code : "subscription_failed";
                        return this.fail(new PgBaseError(code === "cursor_expired" ? 410 : code === "invalid_cursor" ? 400 : 0, code));
                    }
                    if (message.event === "PB_CONNECT") {
                        this.clientId = typeof data === "object" && data !== null && "clientId" in data ? String(data.clientId ?? "") : "";
                        if (!this.clientId)
                            throw new PgBaseError(0, "invalid_realtime_connect");
                        await this.sync();
                        this.resolveReady?.();
                        this.readySettled = true;
                        this.notify({ state: "connected", attempt: reconnects });
                        continue;
                    }
                    if (message.id)
                        this.lastEventId = canonicalDeliveryCursor(message.id);
                    const topic = message.event;
                    if (!topic)
                        continue;
                    const event = topic.startsWith("@")
                        ? { topic: topic, data }
                        : recordRealtimeEvent(topic, data);
                    if (!event)
                        continue;
                    for (const listener of [...(this.listeners.get(topic) ?? [])]) {
                        this.deliver(listener, event, topic);
                    }
                    if (this.listeners.has(topic))
                        reconnects = 0;
                }
            }
            catch (error) {
                if (controller.signal.aborted)
                    return;
                reconnectError = error;
                if (terminalRealtimeError(error))
                    return this.fail(error);
            }
            if (controller.signal.aborted || this.listeners.size === 0)
                return;
            this.clientId = undefined;
            if (reconnects >= this.maxReconnects)
                return this.fail(reconnectError ?? new PgBaseError(0, "realtime_reconnect_exhausted"), "exhausted");
            reconnects += 1;
            this.notify({ state: "reconnecting", attempt: reconnects, source: "transport", error: reconnectError ?? new PgBaseError(0, "realtime_stream_ended") });
            await waitForReconnect(Math.min(this.reconnectDelayMs * 2 ** (reconnects - 1), 10_000), controller.signal);
        }
    }
    fail(error, reason = "terminal") {
        this.clientId = undefined;
        this.listeners.clear();
        this.controller?.abort();
        if (!this.readySettled) {
            this.rejectReady?.(error);
            this.readySettled = true;
        }
        this.notify({ state: "stopped", attempt: this.currentStatus.attempt, reason, ...(reason === "unsubscribed" ? {} : { error, source: "transport" }) });
    }
    listenerFailure(error, topic) {
        this.notify({ ...this.currentStatus, error, source: "listener", topic });
    }
    deliver(listener, event, topic) {
        try {
            Promise.resolve(listener(event)).catch((error) => this.listenerFailure(error, topic));
        }
        catch (error) {
            this.listenerFailure(error, topic);
        }
    }
    notify(status) {
        this.currentStatus = Object.freeze({ ...status, lastEventId: this.lastEventId });
        const snapshot = this.currentStatus;
        for (const observer of [...this.observers]) {
            if (this.currentStatus !== snapshot)
                break;
            this.invokeObserver(observer, snapshot);
        }
    }
    invokeObserver(observer, status) {
        try {
            Promise.resolve(observer(status)).catch(() => { });
        }
        catch { /* An observer cannot affect delivery or recursively report itself. */ }
    }
}
function terminalRealtimeError(error) {
    return error instanceof PgBaseError &&
        ([400, 401, 403, 410].includes(error.status) || error.code === "cursor_expired");
}
function validRealtimeTopic(topic) {
    if (!topic)
        return false;
    if (!topic.startsWith("@"))
        return true;
    return new TextEncoder().encode(topic).length >= 2 &&
        new TextEncoder().encode(topic).length <= 256 &&
        !/[\s\x00-\x1f\x7f?#*]/.test(topic);
}
function applicationWireTopic(topic) {
    const bytes = new TextEncoder().encode(topic);
    if (bytes.length === 0 || bytes.length > 188)
        throw new PgBaseError(0, "invalid_realtime_topic");
    let binary = "";
    for (const byte of bytes)
        binary += String.fromCharCode(byte);
    return `@app/${btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "")}`;
}
function recordRealtimeEvent(topic, data) {
    if (typeof data !== "object" || data === null)
        return undefined;
    const value = data;
    if (!value.record || !["create", "update", "delete"].includes(value.action))
        return undefined;
    return { topic, action: value.action, record: value.record };
}
