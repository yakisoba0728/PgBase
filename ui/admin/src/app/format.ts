import { useMemo } from "react";
import { useLanguage } from "../i18n";

// Display formatting shared by every screen: bytes, durations and server timestamps.

/** "1.4 MB": binary units, one decimal from KB up. */
export function formatBytes(bytes: number): string {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return unit === 0 ? `${value} B` : `${value.toFixed(1)} ${units[unit]}`;
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

/** Formats timestamps (ISO text or epoch ms) in the UI language; `long` adds the date style and seconds. */
export function useTimeFormat() {
  const language = useLanguage();
  return useMemo(() => {
    const short = new Intl.DateTimeFormat(language, { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" });
    const full = new Intl.DateTimeFormat(language, { dateStyle: "medium", timeStyle: "medium" });
    return (value: string | number | undefined, long = false) => {
      if (value === undefined || value === "") return "";
      const date = new Date(value);
      return Number.isNaN(date.getTime()) ? String(value) : (long ? full : short).format(date);
    };
  }, [language]);
}
