import type { PgBaseClient } from "./index.js";
import type { RecordData, RealtimeSubscriptionOptions, RealtimeEvent, RealtimeCustomEvent, RealtimeStatus } from "./types.js";
/** Multiplexes realtime topics over one durable SSE connection. */
export declare class RealtimeService {
    private readonly client;
    private readonly listeners;
    private controller?;
    private clientId?;
    private lastEventId?;
    private running?;
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
    /**
     * Subscribes to an application-owned topic without exposing its reserved
     * realtime wire name. The callback receives the original UTF-8 topic.
     */
    subscribeApplication(topic: string, listener: (event: RealtimeCustomEvent) => void): Promise<() => Promise<void>>;
    unsubscribe(topic?: string, listener?: (event: any) => void): Promise<void>;
    private ensureRunning;
    private sync;
    private run;
    private fail;
    private listenerFailure;
    private deliver;
    private notify;
    private invokeObserver;
}
