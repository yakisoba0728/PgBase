import type { MessageKey } from "../i18n";

/** Colour tone for an HTTP status: only failures get colour. */
export function statusTone(status: number): "danger" | "warning" | undefined {
  if (status >= 500) return "danger";
  if (status >= 400) return "warning";
  return undefined;
}

/** "1h 2m", "3m 4s", "5s" — for lag and ages. */
export function formatDuration(ms: number): string {
  const seconds = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  if (h) return m ? `${h}h ${m}m` : `${h}h`;
  if (m) return s ? `${m}m ${s}s` : `${m}m`;
  return `${s}s`;
}

const pad = (value: string) => value.padStart(2, "0");
const isNumber = (value: string) => /^\d{1,2}$/.test(value);

/** A short reading of common schedules; null when the expression is better read as-is. */
export function describeSchedule(schedule: string): { key: MessageKey; vars?: Record<string, string> } | null {
  const text = schedule.trim();
  const every = /^@every\s+(\S+)$/.exec(text);
  if (every) return { key: "ops.jobs.every", vars: { interval: every[1] } };
  const parts = text.split(/\s+/);
  if (parts.length !== 5) return null;
  const [minute, hour, day, month, weekday] = parts;
  if (day !== "*" || month !== "*") return null;
  const step = /^\*\/(\d+)$/.exec(minute);
  if (step && hour === "*" && weekday === "*") return { key: "ops.jobs.everyMinutes", vars: { count: step[1] } };
  if (!isNumber(minute)) return null;
  if (hour === "*" && weekday === "*") return { key: "ops.jobs.hourly", vars: { minute: pad(minute) } };
  if (!isNumber(hour)) return null;
  const time = `${pad(hour)}:${pad(minute)}`;
  if (weekday === "*") return { key: "ops.jobs.daily", vars: { time } };
  if (weekday === "1-5") return { key: "ops.jobs.weekdays", vars: { time } };
  return null;
}

/** A `datetime-local` value (local wall time) as RFC 3339, or undefined when empty/invalid. */
export function localToRfc3339(value: string): string | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date.toISOString().replace(/\.\d{3}Z$/, "Z");
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const isUuid = (value: string) => uuidPattern.test(value.trim());
