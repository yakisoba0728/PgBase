import { stringify } from "lossless-json";
import type { RecordData, RecordPage } from "../api/records";

/** Records as exported: exactly the stored fields, without the expand data the grid asked for. */
export const exportable = (records: RecordData[]) => records.map(({ expand: _expand, ...record }) => record);

/** Walks the cursor from the first page, stopping after `maxPages`; `complete` says whether it reached the end. */
export async function collectAll(fetchPage: (cursor: string | undefined) => Promise<RecordPage>, maxPages = 100): Promise<{ items: RecordData[]; complete: boolean }> {
  const items: RecordData[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < maxPages; page++) {
    const result = await fetchPage(cursor);
    items.push(...result.items);
    cursor = result.nextCursor;
    if (!cursor) return { items, complete: true };
  }
  return { items, complete: false };
}

/** Runs `task` over `items` with at most `limit` in flight; results line up with `items`. */
export async function settleLimited<T, R>(items: T[], limit: number, task: (item: T) => Promise<R>): Promise<PromiseSettledResult<R>[]> {
  const results: PromiseSettledResult<R>[] = new Array(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const index = next++;
      try {
        results[index] = { status: "fulfilled", value: await task(items[index]) };
      } catch (reason) {
        results[index] = { status: "rejected", reason };
      }
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}

/** Saves `value` as a pretty-printed JSON file (exact numbers kept). */
export function downloadJson(filename: string, value: unknown): void {
  const url = URL.createObjectURL(new Blob([stringify(value, null, 2) ?? "null"], { type: "application/json" }));
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
