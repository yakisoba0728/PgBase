import { describe, expect, it } from "vitest";
import { formatBytes, formatDuration } from "./format";

describe("format", () => {
  it("formats bytes in binary units", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(44817)).toBe("43.8 KB");
    expect(formatBytes(5 * 1024 ** 4)).toBe("5.0 TB");
  });

  it("formats durations", () => {
    expect(formatDuration(-5)).toBe("0s");
    expect(formatDuration(4_900)).toBe("4s");
    expect(formatDuration(120_000)).toBe("2m");
    expect(formatDuration(185_000)).toBe("3m 5s");
    expect(formatDuration(3_600_000 * 2 + 60_000)).toBe("2h 1m");
  });
});
