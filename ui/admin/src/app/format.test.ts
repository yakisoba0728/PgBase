import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { setLanguage } from "../i18n";
import { formatBytes, formatDuration, useTimeFormat } from "./format";

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

  it("adds the year to a short timestamp from another year, so it never reads as this year", () => {
    setLanguage("en");
    const { result } = renderHook(() => useTimeFormat());
    const thisYear = new Date().getFullYear();
    const at = (year: number) => result.current(new Date(year, 10, 18, 8, 19, 4).getTime());
    expect(at(thisYear + 1)).toContain(String(thisYear + 1).slice(-2));
    expect(at(thisYear + 1).length).toBeGreaterThan(at(thisYear).length);
  });
});
