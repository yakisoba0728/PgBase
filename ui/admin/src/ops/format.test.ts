import { describeSchedule, localToRfc3339, statusTone } from "./format";
import { logFilterFrom } from "./queries";

describe("ops format", () => {
  it("colours only failing statuses", () => {
    expect(statusTone(200)).toBeUndefined();
    expect(statusTone(304)).toBeUndefined();
    expect(statusTone(404)).toBe("warning");
    expect(statusTone(503)).toBe("danger");
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

  it("reads only valid filters from the URL", () => {
    const filter = logFilterFrom(new URLSearchParams("status=9&method=POST&route=/api/x&from=bad"));
    expect(filter).toEqual({ statusClass: undefined, method: "POST", route: "/api/x", q: undefined, from: undefined, to: undefined });
    expect(logFilterFrom(new URLSearchParams("q=%20192.0.2.4%20")).q).toBe("192.0.2.4");
    expect(logFilterFrom(new URLSearchParams("q=%20%20")).q).toBeUndefined();
    expect(logFilterFrom(new URLSearchParams("status=4")).statusClass).toBe(4);
  });
});
