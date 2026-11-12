import { describeSchedule, formatDuration, isUuid, localToRfc3339, statusTone } from "./format";
import { logFilterFrom } from "./queries";

describe("ops format", () => {
  it("colours only failing statuses", () => {
    expect(statusTone(200)).toBeUndefined();
    expect(statusTone(304)).toBeUndefined();
    expect(statusTone(404)).toBe("warning");
    expect(statusTone(503)).toBe("danger");
  });

  it("formats durations compactly", () => {
    expect(formatDuration(-5)).toBe("0s");
    expect(formatDuration(4_900)).toBe("4s");
    expect(formatDuration(120_000)).toBe("2m");
    expect(formatDuration(185_000)).toBe("3m 5s");
    expect(formatDuration(3_600_000 * 2 + 60_000)).toBe("2h 1m");
  });

  it("summarises common schedules and leaves the rest alone", () => {
    expect(describeSchedule("@every 1h")).toEqual({ key: "ops.jobs.every", vars: { interval: "1h" } });
    expect(describeSchedule("*/15 * * * *")).toEqual({ key: "ops.jobs.everyMinutes", vars: { count: "15" } });
    expect(describeSchedule("5 * * * *")).toEqual({ key: "ops.jobs.hourly", vars: { minute: "05" } });
    expect(describeSchedule("0 2 * * *")).toEqual({ key: "ops.jobs.daily", vars: { time: "02:00" } });
    expect(describeSchedule("30 9 * * 1-5")).toEqual({ key: "ops.jobs.weekdays", vars: { time: "09:30" } });
    expect(describeSchedule("0 2 1 * *")).toBeNull();
    expect(describeSchedule("0 2 * * 0")).toBeNull();
    expect(describeSchedule("garbage")).toBeNull();
  });

  it("converts local input to RFC 3339 without milliseconds", () => {
    expect(localToRfc3339("")).toBeUndefined();
    expect(localToRfc3339("nope")).toBeUndefined();
    expect(localToRfc3339("2026-11-12T10:30:00")).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/);
  });

  it("validates event ids", () => {
    expect(isUuid(" 9e0a974f-b40a-4585-83b9-744ca949fc31 ")).toBe(true);
    expect(isUuid("9e0a974f")).toBe(false);
  });

  it("reads only valid filters from the URL", () => {
    const filter = logFilterFrom(new URLSearchParams("status=9&method=POST&route=/api/x&from=bad"));
    expect(filter).toEqual({ statusClass: undefined, method: "POST", route: "/api/x", from: undefined, to: undefined });
    expect(logFilterFrom(new URLSearchParams("status=4")).statusClass).toBe(4);
  });
});
