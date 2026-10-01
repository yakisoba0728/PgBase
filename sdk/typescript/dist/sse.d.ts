import type { RecordEvent } from "./types.js";
export declare function canonicalDeliveryCursor(value: string | number): string;
export type SSEMessage = {
    event?: string;
    id?: string;
    data: string;
};
export declare function parseEventStream(body: ReadableStream<Uint8Array>, signal?: AbortSignal): AsyncGenerator<SSEMessage>;
export declare function parseSSEMessage(raw: string): SSEMessage | undefined;
export declare function deliveryCursor(message: SSEMessage): string | undefined;
export declare function recordEvent(message: SSEMessage, deliveryId: string): RecordEvent | undefined;
export declare function waitForReconnect(delay: number, signal?: AbortSignal): Promise<void>;
