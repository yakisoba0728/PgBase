/**
 * Runs `task` over `items` with at most `limit` in flight; results line up with `items`.
 * `onSettled` hears of each one as it finishes, for progress.
 */
export async function settleLimited<T, R>(
  items: T[],
  limit: number,
  task: (item: T) => Promise<R>,
  onSettled?: (item: T, result: PromiseSettledResult<R>) => void,
): Promise<PromiseSettledResult<R>[]> {
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
      onSettled?.(items[index], results[index]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}
