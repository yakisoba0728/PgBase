import type { PgBaseClient } from "./index.js";
import { PgBaseError } from "./types.js";
import type { RecordData, RealtimeSubscriptionOptions, RealtimeEvent, RealtimeCustomEvent, RealtimeSubscriptionEvent, RealtimeStatus } from "./types.js";
import { parseEventStream, canonicalDeliveryCursor, waitForReconnect } from "./sse.js";

/** Multiplexes realtime topics over one durable SSE connection. */
export class RealtimeService {
  private readonly listeners = new Map<string, Set<(event: any) => void>>();
  private controller?: AbortController;
  private clientId?: string;
  private lastEventId?: string;
  private running?: Promise<void>;
  private ready?: Promise<void>;
  private resolveReady?: () => void;
  private rejectReady?: (error: unknown) => void;
  private readySettled = false;
  private maxReconnects = 5;
  private reconnectDelayMs = 250;
  private readonly observers = new Set<(status: RealtimeStatus) => void | Promise<void>>();
  private currentStatus: RealtimeStatus = Object.freeze({ state: "stopped", attempt: 0 });

  constructor(private readonly client: PgBaseClient) {}

  get status(): RealtimeStatus { return Object.freeze({ ...this.currentStatus, lastEventId: this.lastEventId }); }

  /** Observes the shared session, starting with its current snapshot. */
  onStatus(observer: (status: RealtimeStatus) => void | Promise<void>): () => void {
    this.observers.add(observer);
    this.invokeObserver(observer, this.status);
    return () => { this.observers.delete(observer); };
  }

  /** Stops all subscriptions and forgets the replay cursor for an explicit resync. */
  async resetCursor(): Promise<void> {
    await this.unsubscribe();
    await this.running;
    this.lastEventId = undefined;
    this.notify({ state: "stopped", attempt: 0, reason: "reset" });
  }

  async subscribe(topic: `@${string}`, listener: (event: RealtimeCustomEvent) => void, options?: RealtimeSubscriptionOptions): Promise<() => Promise<void>>;
  async subscribe<T extends RecordData = RecordData>(topic: string, listener: (event: RealtimeEvent<T>) => void, options?: RealtimeSubscriptionOptions): Promise<() => Promise<void>>;
  async subscribe(topic: string, listener: (event: any) => void, options: RealtimeSubscriptionOptions = {}): Promise<() => Promise<void>> {
    if (!validRealtimeTopic(topic)) throw new PgBaseError(0, "invalid_realtime_topic");
    if (options.maxReconnects !== undefined && (!Number.isInteger(options.maxReconnects) || options.maxReconnects < 0)) throw new PgBaseError(0, "invalid_reconnect_options");
    if (options.reconnectDelayMs !== undefined && (!Number.isFinite(options.reconnectDelayMs) || options.reconnectDelayMs < 0)) throw new PgBaseError(0, "invalid_reconnect_options");
    if (this.controller?.signal.aborted && this.running) await this.running;
    let set = this.listeners.get(topic);
    if (!set) { set = new Set(); this.listeners.set(topic, set); }
    set.add(listener);
    this.ensureRunning(options);
    if (this.clientId) await this.sync();
    else await this.ready;
    return async () => this.unsubscribe(topic, listener);
  }

  /**
   * Subscribes to an application-owned topic without exposing its reserved
   * realtime wire name. The callback receives the original UTF-8 topic.
   */
  async subscribeApplication(topic: string, listener: (event: RealtimeCustomEvent) => void): Promise<() => Promise<void>> {
    const wireTopic = applicationWireTopic(topic);
    return this.subscribe(wireTopic, (event) => this.deliver(listener, { topic, data: event.data }, topic));
  }

  async unsubscribe(topic?: string, listener?: (event: any) => void): Promise<void> {
    if (topic === undefined) this.listeners.clear();
    else if (listener === undefined) this.listeners.delete(topic);
    else { const set = this.listeners.get(topic); set?.delete(listener); if (set?.size === 0) this.listeners.delete(topic); }
    if (this.listeners.size === 0) {
      this.controller?.abort();
      this.clientId = undefined;
      this.fail(new PgBaseError(0, "realtime_unsubscribed"), "unsubscribed");
      return;
    }
    if (this.clientId) await this.sync();
  }

  private ensureRunning(options: RealtimeSubscriptionOptions): void {
    if (this.running) return;
    this.maxReconnects = options.maxReconnects ?? 5;
    this.reconnectDelayMs = options.reconnectDelayMs ?? 250;
    this.readySettled = false;
    this.ready = new Promise<void>((resolve, reject) => { this.resolveReady = resolve; this.rejectReady = reject; });
    const controller = this.controller = new AbortController();
    this.running = Promise.resolve().then(() => this.run(controller)).finally(() => { this.running = undefined; this.ready = undefined; this.resolveReady = undefined; this.rejectReady = undefined; });
  }
  private async sync(): Promise<void> { if (this.clientId) await this.client._request<void>("/api/realtime", { method: "POST", body: JSON.stringify({ clientId: this.clientId, subscriptions: [...this.listeners.keys()] }) }, this.controller?.signal); }
  private async run(controller: AbortController): Promise<void> {
    if (this.listeners.size === 0) return;
    let reconnects = 0;
    this.notify({ state: "connecting", attempt: 0 });
    while (!controller.signal.aborted && this.listeners.size) {
      let reconnectError: unknown;
      try {
        const response = await this.client._eventStream("/api/realtime", this.lastEventId, controller.signal);
        for await (const message of parseEventStream(response.body!, controller.signal)) {
          const data = JSON.parse(message.data) as unknown;
          if (message.event === "PB_ERROR") {
            const code = typeof data === "object" && data !== null && "code" in data && typeof data.code === "string" ? data.code : "subscription_failed";
            return this.fail(new PgBaseError(code === "cursor_expired" ? 410 : code === "invalid_cursor" ? 400 : 0, code));
          }
          if (message.event === "PB_CONNECT") {
            this.clientId = typeof data === "object" && data !== null && "clientId" in data ? String(data.clientId ?? "") : "";
            if (!this.clientId) throw new PgBaseError(0, "invalid_realtime_connect");
            await this.sync();
            this.resolveReady?.();
            this.readySettled = true;
            this.notify({ state: "connected", attempt: reconnects });
            continue;
          }
          if (message.id) this.lastEventId = canonicalDeliveryCursor(message.id);
          const topic = message.event;
          if (!topic) continue;
          const event = topic.startsWith("@")
            ? { topic: topic as `@${string}`, data }
            : recordRealtimeEvent(topic, data);
          if (!event) continue;
          for (const listener of [...(this.listeners.get(topic) ?? [])]) {
            this.deliver(listener, event, topic);
          }
          if (this.listeners.has(topic)) reconnects = 0;
        }
      } catch (error) {
        if (controller.signal.aborted) return;
        reconnectError = error;
        if (terminalRealtimeError(error)) return this.fail(error);
      }
      if (controller.signal.aborted || this.listeners.size === 0) return;
      this.clientId = undefined;
      if (reconnects >= this.maxReconnects) return this.fail(reconnectError ?? new PgBaseError(0, "realtime_reconnect_exhausted"), "exhausted");
      reconnects += 1;
      this.notify({ state: "reconnecting", attempt: reconnects, source: "transport", error: reconnectError ?? new PgBaseError(0, "realtime_stream_ended") });
      await waitForReconnect(Math.min(this.reconnectDelayMs * 2 ** (reconnects - 1), 10_000), controller.signal);
    }
  }

  private fail(error: unknown, reason: RealtimeStatus["reason"] = "terminal"): void {
    this.clientId = undefined;
    this.listeners.clear();
    this.controller?.abort();
    if (!this.readySettled) {
      this.rejectReady?.(error);
      this.readySettled = true;
    }
    this.notify({ state: "stopped", attempt: this.currentStatus.attempt, reason, ...(reason === "unsubscribed" ? {} : { error, source: "transport" as const }) });
  }

  private listenerFailure(error: unknown, topic: string): void {
    this.notify({ ...this.currentStatus, error, source: "listener", topic });
  }

  private deliver(listener: (event: any) => unknown, event: unknown, topic: string): void {
    try { Promise.resolve(listener(event)).catch((error) => this.listenerFailure(error, topic)); }
    catch (error) { this.listenerFailure(error, topic); }
  }

  private notify(status: RealtimeStatus): void {
    this.currentStatus = Object.freeze({ ...status, lastEventId: this.lastEventId });
    const snapshot = this.currentStatus;
    for (const observer of [...this.observers]) {
      if (this.currentStatus !== snapshot) break;
      this.invokeObserver(observer, snapshot);
    }
  }

  private invokeObserver(observer: (status: RealtimeStatus) => void | Promise<void>, status: RealtimeStatus): void {
    try { Promise.resolve(observer(status)).catch(() => {}); }
    catch { /* An observer cannot affect delivery or recursively report itself. */ }
  }
}

function terminalRealtimeError(error: unknown): boolean {
  return error instanceof PgBaseError &&
    ([400, 401, 403, 410].includes(error.status) || error.code === "cursor_expired");
}

function validRealtimeTopic(topic: string): boolean {
  if (!topic) return false;
  if (!topic.startsWith("@")) return true;
  return new TextEncoder().encode(topic).length >= 2 &&
    new TextEncoder().encode(topic).length <= 256 &&
    !/[\s\x00-\x1f\x7f?#*]/.test(topic);
}

function applicationWireTopic(topic: string): `@app/${string}` {
  const bytes = new TextEncoder().encode(topic);
  if (bytes.length === 0 || bytes.length > 188) throw new PgBaseError(0, "invalid_realtime_topic");
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return `@app/${btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "")}`;
}

function recordRealtimeEvent(topic: string, data: unknown): RealtimeEvent | undefined {
  if (typeof data !== "object" || data === null) return undefined;
  const value = data as Record<string, unknown>;
  if (!value.record || !["create", "update", "delete"].includes(value.action as string)) return undefined;
  return { topic, action: value.action as RealtimeEvent["action"], record: value.record as RecordData };
}
