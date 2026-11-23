import type { PgBaseClient } from "./index.js";
import type { RecordData, RealtimeSubscriptionOptions, RealtimeEvent, RealtimeCustomEvent, RealtimeSubscriptionEvent, RealtimeStatus } from "./types.js";
/** Multiplexes realtime topics over one durable SSE connection. */
export declare class RealtimeService {
    private readonly client;
    private readonly listeners;
    private controller?;
    private clientId?;
    private lastEventId?;
    private running?;
    private readonly queued;
    private draining;
    private syncing;
    private ready?;
    private resolveReady?;
    private rejectReady?;
    private readySettled;
    private maxReconnects;
    private reconnectDelayMs;
    private readonly observers;
    private currentStatus;
    constructor(client: PgBaseClient);
    get status(): RealtimeStatus;
    /** Observes the shared session, starting with its current snapshot. */
    onStatus(observer: (status: RealtimeStatus) => void | Promise<void>): () => void;
    /** Stops all subscriptions and forgets the replay cursor for an explicit resync. */
    resetCursor(): Promise<void>;
    subscribe(topic: `@${string}`, listener: (event: RealtimeCustomEvent) => void, options?: RealtimeSubscriptionOptions): Promise<() => Promise<void>>;
    subscribe<T extends RecordData = RecordData>(topic: string, listener: (event: RealtimeEvent<T>) => void, options?: RealtimeSubscriptionOptions): Promise<() => Promise<void>>;
    /** Registers all topics with one atomic server replacement. */
    subscribeMany(topics: readonly string[], listener: (event: RealtimeSubscriptionEvent) => void, options?: RealtimeSubscriptionOptions): Promise<() => Promise<void>>;
    private subscribeTopics;
    /**
     * Installs queued subscriptions in call order. Requests that queue behind an
     * in-flight one share a single replacement request. A failed shared
     * replacement leaves the server unchanged, so each request is then retried
     * alone and only the failing one rejects, as if they had run serially.
     */
    private drain;
    /**
     * Registers the requests' listeners and replaces the server topic list once.
     * A shared failure rolls every request back; `stopWhenEmpty` is false when
     * the caller retries them, so the stream is not torn down in between.
     */
    private subscribeNow;
    private removeListener;
    /**
     * Subscribes to an application-owned topic without exposing its reserved
     * realtime wire name. The callback receives the original UTF-8 topic.
     */
    subscribeApplication(topic: string, listener: (event: RealtimeCustomEvent) => void): Promise<() => Promise<void>>;
    unsubscribe(topic?: string, listener?: (event: any) => void): Promise<void>;
    private updateSubscriptions;
    private ensureRunning;
    private newReadiness;
    private sync;
    private run;
    private fail;
    private listenerFailure;
    private deliver;
    private notify;
    private invokeObserver;
}
