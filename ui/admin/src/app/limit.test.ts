import { expect, it } from "vitest";
import { settleLimited } from "./limit";

it("limits concurrency, keeps results in order and reports each one as it settles", async () => {
  let running = 0;
  let peak = 0;
  const settled: number[] = [];
  const results = await settleLimited([1, 2, 3, 4, 5], 2, async (n) => {
    peak = Math.max(peak, ++running);
    await new Promise((resolve) => setTimeout(resolve, 1));
    running--;
    if (n === 3) throw new Error("no");
    return n * 2;
  }, (n) => settled.push(n));
  expect(peak).toBe(2);
  expect(results.map((result) => result.status)).toEqual(["fulfilled", "fulfilled", "rejected", "fulfilled", "fulfilled"]);
  expect(settled.sort()).toEqual([1, 2, 3, 4, 5]);
});
