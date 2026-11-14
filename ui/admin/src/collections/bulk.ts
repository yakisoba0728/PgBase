import { ApiError } from "../api/http";
import { settleLimited } from "../app/limit";
import type { RecordData, RecordPage } from "../api/records";

/** Records as exported: exactly the stored fields, without the expand data the grid asked for. */
export const exportable = (records: RecordData[]) => records.map(({ expand: _expand, ...record }) => record);

/**
 * Walks the cursor from the first page, stopping after `maxPages`; `complete` says whether it reached the end.
 * `onPage` gets the rows so far (the same array, so a caller keeps them if a later page fails) and the page;
 * an aborted `signal` stops before the next page.
 */
export async function collectAll(
  fetchPage: (cursor: string | undefined) => Promise<RecordPage>,
  maxPages = 100,
  options: { signal?: AbortSignal; onPage?: (items: RecordData[], page: RecordPage) => void } = {},
): Promise<{ items: RecordData[]; complete: boolean }> {
  const items: RecordData[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < maxPages; page++) {
    options.signal?.throwIfAborted();
    const result = await fetchPage(cursor);
    items.push(...result.items);
    options.onPage?.(items, result);
    cursor = result.nextCursor;
    if (!cursor) return { items, complete: true };
  }
  return { items, complete: false };
}

export interface DeleteOutcome {
  deleted: string[];
  failed: string[];
  /** The first failure's error. */
  reason?: unknown;
  /** Stopped before every id was tried; the untried ones were left alone. */
  stopped: boolean;
}

const singleChunk = 24;
// Marks a single delete that was never sent because the run was stopped.
const skipped = Symbol("skipped");

/**
 * Deletes `ids` chunk by chunk: one batch request per chunk while `batchSize` > 1, else single deletes six at a time.
 * A batch is all or nothing, so a refused one is retried record by record to find the ones at fault; a refused
 * batch shape (disabled, or over the limit) switches to single deletes for the rest. A 404 counts as deleted.
 * Once `signal` aborts no further request is sent; requests already sent finish and are counted.
 */
export async function deleteAll(
  ids: string[],
  options: { batchSize: number; batch: (ids: string[]) => Promise<void>; one: (id: string) => Promise<void>; signal: AbortSignal; onProgress: (deleted: number, failed: number) => void },
): Promise<DeleteOutcome> {
  const outcome: DeleteOutcome = { deleted: [], failed: [], stopped: false };
  let size = options.batchSize > 1 ? options.batchSize : 0;
  let at = 0;
  while (at < ids.length && !options.signal.aborted) {
    const chunk = ids.slice(at, at + (size || singleChunk));
    let singles = chunk;
    if (size) {
      try {
        await options.batch(chunk);
        outcome.deleted.push(...chunk);
        singles = [];
      } catch (error) {
        if (error instanceof ApiError && (error.code === "batch_disabled" || error.status === 400)) size = 0;
      }
    }
    const results = await settleLimited(singles, 6, (id) => (options.signal.aborted ? Promise.reject(skipped) : options.one(id)));
    results.forEach((result, index) => {
      if (result.status === "rejected" && result.reason === skipped) return;
      if (result.status === "fulfilled" || (result.reason instanceof ApiError && result.reason.status === 404)) outcome.deleted.push(singles[index]);
      else {
        outcome.failed.push(singles[index]);
        outcome.reason ??= result.reason;
      }
    });
    at += chunk.length;
    options.onProgress(outcome.deleted.length, outcome.failed.length);
  }
  outcome.stopped = outcome.deleted.length + outcome.failed.length < ids.length;
  return outcome;
}
