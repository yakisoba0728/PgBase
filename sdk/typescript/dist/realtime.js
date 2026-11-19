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
    subscribing;
    syncing = Promise.resolve();
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
        return this.subscribeTopics([topic], listener, options);
    }
    /** Registers all topics with one atomic server replacement. */
    subscribeMany(topics, listener, options = {}) {
        return this.subscribeTopics([...new Set(topics)], listener, options);
    }
    subscribeTopics(topics, listener, options) {
        const generation = this.client.auth.generation;
        const start = () => {
            if (generation !== this.client.auth.generation)
                throw new PgBaseError(0, "realtime_unsubscribed");
            return this.subscribeNow(topics, listener, options, generation);
        };
        const result = this.subscribing ? this.subscribing.then(start) : start();
        const tail = result.catch(() => { });
        this.subscribing = tail;
        void tail.then(() => { if (this.subscribing === tail)
            this.subscribing = undefined; });
        return result;
    }
    async subscribeNow(topics, listener, options, generation) {
        if (topics.length === 0 || topics.some((topic) => !validRealtimeTopic(topic)))
            throw new PgBaseError(0, "invalid_realtime_topic");
        if (options.maxReconnects !== undefined && (!Number.isInteger(options.maxReconnects) || options.maxReconnects < 0))
            throw new PgBaseError(0, "invalid_reconnect_options");
        if (options.reconnectDelayMs !== undefined && (!Number.isFinite(options.reconnectDelayMs) || options.reconnectDelayMs < 0))
            throw new PgBaseError(0, "invalid_reconnect_options");
        if (this.controller?.signal.aborted && this.running)
            await this.running;
        // Reinstall established topics first. A denied new topic must not make the
        // reconnect that restores the old topics fail.
        const previousController = this.controller;
        while (this.running && !this.readySettled && !previousController?.signal.aborted)
            await this.ready;
        if (generation !== this.client.auth.generation || (this.running && previousController?.signal.aborted))
            throw new PgBaseError(0, "realtime_unsubscribed");
        const registrations = topics.map((topic) => {
            let set = this.listeners.get(topic);
            if (!set) {
                set = new Set();
                this.listeners.set(topic, set);
            }
            const added = !set.has(listener);
            set.add(listener);
            return { topic, set, added };
        });
        this.ensureRunning(options);
        const controller = this.controller;
        try {
            if (this.clientId) {
                if (!await this.sync())
                    throw new PgBaseError(0, "realtime_connection_changed");
            }
            else
                await this.ready;
            if (generation !== this.client.auth.generation || controller?.signal.aborted || registrations.some(({ topic, set }) => this.listeners.get(topic) !== set || !set.has(listener))) {
                throw new PgBaseError(0, "realtime_unsubscribed");
            }
        }
        catch (error) {
            for (const { topic, set, added } of registrations)
                if (added)
                    this.removeListener(topic, set, listener);
            if (this.listeners.size === 0 && !this.controller?.signal.aborted)
                await this.unsubscribe();
            throw error;
        }
        return async () => {
            if (this.controller !== controller)
                return;
            for (const { topic, set } of registrations)
                this.removeListener(topic, set, listener);
            await this.updateSubscriptions();
        };
    }
    removeListener(topic, set, listener) {
        if (this.listeners.get(topic) !== set)
            return;
        set.delete(listener);
        if (set.size === 0)
            this.listeners.delete(topic);
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
        await this.updateSubscriptions();
    }
    async updateSubscriptions() {
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
        this.newReadiness();
        const controller = this.controller = new AbortController();
        const stopOnAuthChange = this.client.auth._onChange(() => { void this.unsubscribe(); });
        this.running = Promise.resolve().then(() => this.run(controller)).finally(() => { stopOnAuthChange(); this.running = undefined; this.ready = undefined; this.resolveReady = undefined; this.rejectReady = undefined; });
    }
    newReadiness() {
        this.readySettled = false;
        this.ready = new Promise((resolve, reject) => { this.resolveReady = resolve; this.rejectReady = reject; });
        // A reconnect can exhaust its budget with nobody awaiting readiness.
        void this.ready.catch(() => { });
    }
    sync() {
        const controller = this.controller;
        const clientId = this.clientId;
        const result = this.syncing.then(async () => {
            if (controller !== this.controller || controller?.signal.aborted || !clientId || clientId !== this.clientId)
                return false;
            await this.client._request("/api/realtime", { method: "POST", body: JSON.stringify({ clientId, subscriptions: [...this.listeners.keys()] }) }, controller?.signal);
            return true;
        });
        this.syncing = result.catch(() => { });
        return result;
    }
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
                    if (controller.signal.aborted)
                        return;
                    const data = JSON.parse(message.data);
                    if (message.event === "PB_ERROR") {
                        const code = typeof data === "object" && data !== null && "code" in data && typeof data.code === "string" ? data.code : "subscription_failed";
                        const error = new PgBaseError(code === "cursor_expired" ? 410 : code === "invalid_cursor" ? 400 : 0, code);
                        if (code === "subscription_failed" && typeof data === "object" && data !== null && "retryable" in data && data.retryable === true)
                            throw error;
                        return this.fail(error);
                    }
                    if (message.event === "PB_CONNECT") {
                        this.clientId = typeof data === "object" && data !== null && "clientId" in data ? String(data.clientId ?? "") : "";
                        if (!this.clientId)
                            throw new PgBaseError(0, "invalid_realtime_connect");
                        await this.sync();
                        if (controller.signal.aborted)
                            return;
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
                        if (controller.signal.aborted)
                            return;
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
            if (this.readySettled)
                this.newReadiness();
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
        const controller = this.controller;
        const generation = this.client.auth.generation;
        // Listener promises may settle after their transport has stopped or the
        // shared service has connected as another identity.
        const failed = (error) => {
            if (controller === this.controller && controller && !controller.signal.aborted && generation === this.client.auth.generation) {
                this.listenerFailure(error, topic);
            }
        };
        try {
            Promise.resolve(listener(event)).catch(failed);
        }
        catch (error) {
            failed(error);
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
