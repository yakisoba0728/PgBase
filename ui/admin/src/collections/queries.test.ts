import { describe, expect, it } from "vitest";
import { pooled } from "./queries";

describe("pooled", () => {
  it("runs at most four tasks at once and skips tasks cancelled while queued", async () => {
    let running = 0;
    let peak = 0;
    const releases: (() => void)[] = [];
    const ran: number[] = [];
    const task = (n: number) => () =>
      new Promise<number>((resolve) => {
        running++;
        peak = Math.max(peak, running);
        ran.push(n);
        releases.push(() => {
          running--;
          resolve(n);
        });
      });
    const cancelled = new AbortController();
    const results = Array.from({ length: 8 }, (_, n) => pooled(n === 5 ? cancelled.signal : undefined, task(n)));
    await Promise.resolve();
    expect(ran).toEqual([0, 1, 2, 3]);
    cancelled.abort();
    await expect(results[5]).rejects.toBeDefined();
    while (releases.length) {
      releases.shift()!();
      await new Promise((resolve) => setTimeout(resolve));
    }
    expect(await Promise.all(results.filter((_, n) => n !== 5))).toEqual([0, 1, 2, 3, 4, 6, 7]);
    expect(peak).toBe(4);
    expect(ran).not.toContain(5);
  });
});
