import { describe, expect, it } from "vitest";
import { pushRecent } from "./recent";

describe("recent collections", () => {
  it("keeps the most recent first, without duplicates, up to the limit", () => {
    expect(pushRecent([], "a")).toEqual(["a"]);
    expect(pushRecent(["a", "b"], "b")).toEqual(["b", "a"]);
    expect(pushRecent(["a", "b", "c"], "d", 3)).toEqual(["d", "a", "b"]);
  });
});
