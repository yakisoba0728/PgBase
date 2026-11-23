import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ToggleEvent } from "react";
import { RiArrowLeftSLine, RiArrowRightSLine, RiCalendarLine } from "@remixicon/react";
import { useLanguage, useT } from "../i18n";
import { Button, IconButton } from "./Button";
import { placeNear } from "./Popover";

// Native date inputs render differently per browser and OS locale (and datetime-local has no picker at all in some),
// so dates are typed and shown in one fixed form, YYYY-MM-DD HH:mm:ss, with a calendar beside it whose month and
// weekday names follow the UI language. Values are wall times without a zone, the same text datetime-local gives.

interface Parts {
  year: number;
  month: number; // 1-12
  day: number;
  hour: number;
  minute: number;
  second: number;
  /** Fractional seconds as written (".123"), kept so editing a stored value does not drop its precision. */
  fraction: string;
}

const pad = (value: number, width = 2) => String(value).padStart(width, "0");
const daysIn = (year: number, month: number) => new Date(Date.UTC(year, month, 0)).getUTCDate();
const pattern = /^(\d{4})[-./] *(\d{1,2})[-./] *(\d{1,2})\.?(?:[ T]+(\d{1,2}):(\d{1,2})(?::(\d{1,2})(\.\d+)?)?)?$/;

/** Reads typed or stored text ("2026-11-24", "2026-11-24 9:05", "2026. 11. 24. 09:05:00", "2026-11-24T09:05:00.5"). */
export function parseDateTime(text: string): Parts | null {
  const match = pattern.exec(text.trim());
  if (!match) return null;
  const [year, month, day, hour = 0, minute = 0, second = 0] = match.slice(1, 7).map((part) => (part === undefined ? undefined : Number(part)));
  const parts = { year: year!, month: month!, day: day!, hour, minute, second, fraction: match[7] ?? "" } as Parts;
  if (parts.month < 1 || parts.month > 12 || parts.day < 1 || parts.day > daysIn(parts.year, parts.month)) return null;
  if (parts.hour > 23 || parts.minute > 59 || parts.second > 59) return null;
  return parts;
}

const datePart = (parts: Parts) => `${pad(parts.year, 4)}-${pad(parts.month)}-${pad(parts.day)}`;
const timePart = (parts: Parts) => `${pad(parts.hour)}:${pad(parts.minute)}:${pad(parts.second)}${parts.fraction}`;

/** The value handed to onChange: "YYYY-MM-DD", or "YYYY-MM-DDTHH:mm:ss" (plus any fraction) with time. */
export function formatValue(parts: Parts, time: boolean): string {
  return time ? `${datePart(parts)}T${timePart(parts)}` : datePart(parts);
}

/** The text shown in the box: the value with a space for the T; text that does not parse is shown as it is. */
export function displayText(value: string, time: boolean): string {
  const parts = parseDateTime(value);
  if (!parts) return value;
  return time ? `${datePart(parts)} ${timePart(parts)}` : datePart(parts);
}

/** The current wall time, in UTC or in the browser's zone. */
function now(utc: boolean): Parts {
  const date = new Date();
  return utc
    ? { year: date.getUTCFullYear(), month: date.getUTCMonth() + 1, day: date.getUTCDate(), hour: date.getUTCHours(), minute: date.getUTCMinutes(), second: date.getUTCSeconds(), fraction: "" }
    : { year: date.getFullYear(), month: date.getMonth() + 1, day: date.getDate(), hour: date.getHours(), minute: date.getMinutes(), second: date.getSeconds(), fraction: "" };
}

export interface DateTimeInputProps {
  /** "YYYY-MM-DD" or "YYYY-MM-DDTHH:mm[:ss[.fff]]"; "" for none. Text that does not parse is shown as it is. */
  value: string;
  /** Called with the normalised value when the text parses, "" when cleared, and the raw text otherwise. */
  onChange: (value: string) => void;
  /** With a time of day (the default) or a date only. */
  time?: boolean;
  /** "Now", "Today" and the calendar's today mark read UTC instead of the browser's zone (the value is UTC wall time). */
  utc?: boolean;
  size?: "md" | "sm";
  id?: string;
  name?: string;
  disabled?: boolean;
  required?: boolean;
  invalid?: boolean;
  className?: string;
  "aria-label"?: string;
  "aria-describedby"?: string;
}

/** A date or date-and-time box: typed as YYYY-MM-DD HH:mm:ss, or chosen from a calendar (Alt+↓ or the button opens it). */
export function DateTimeInput({ value, onChange, time = true, utc = false, size, id: ownId, name, disabled, required, invalid, className, "aria-label": label, "aria-describedby": describedBy }: DateTimeInputProps) {
  const t = useT();
  const autoId = useId();
  const id = ownId ?? autoId;
  const panelId = `${id}-calendar`;
  const wrap = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [text, setText] = useState(() => displayText(value, time));
  // A new value from outside replaces the text, unless the text already means that value (mid-edit "2026-1-5").
  useEffect(() => {
    const parts = parseDateTime(text);
    if ((parts ? formatValue(parts, time) : text.trim()) !== value) setText(displayText(value, time));
    // Only an outside change re-syncs the text; typing alone never rewrites it.
  }, [value, time]);
  const typedInvalid = text.trim() !== "" && parseDateTime(text) === null;

  const type = (next: string) => {
    setText(next);
    const parts = parseDateTime(next);
    onChange(parts ? formatValue(parts, time) : next.trim());
  };
  const choose = (parts: Parts | null) => {
    const next = parts ? formatValue(parts, time) : "";
    setText(displayText(next, time));
    onChange(next);
  };
  const close = () => panel.current?.hidePopover?.();

  const onBeforeToggle = (event: ToggleEvent<HTMLDivElement>) => {
    const anchor = wrap.current?.getBoundingClientRect();
    if (event.newState === "open" && panel.current && anchor) placeNear(panel.current, anchor, "start", panel.current.offsetWidth || 296);
  };
  const onToggle = (event: ToggleEvent<HTMLDivElement>) => {
    const opening = event.newState === "open";
    setOpen(opening);
    // Focus returns to the box when the calendar closes with focus inside it (Escape, a pick, Done).
    if (!opening && (panel.current?.contains(document.activeElement) || document.activeElement === document.body)) input.current?.focus();
  };
  const onInputKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.altKey && event.key === "ArrowDown") {
      event.preventDefault();
      panel.current?.showPopover?.();
    }
  };

  return (
    <div ref={wrap} className={["date-input", size === "sm" && "control-sm", className].filter(Boolean).join(" ")} data-date-only={time ? undefined : ""}>
      <input
        ref={input}
        id={id}
        name={name}
        className={["field-control", "mono", size === "sm" && "control-sm"].filter(Boolean).join(" ")}
        type="text"
        inputMode="numeric"
        autoComplete="off"
        spellCheck={false}
        placeholder={time ? "YYYY-MM-DD HH:mm:ss" : "YYYY-MM-DD"}
        value={text}
        disabled={disabled}
        required={required}
        aria-label={label}
        aria-describedby={describedBy}
        aria-invalid={invalid || typedInvalid || undefined}
        title={typedInvalid ? t(time ? "date.invalidTime" : "date.invalid") : undefined}
        onChange={(event) => type(event.target.value)}
        onBlur={() => { const parts = parseDateTime(text); if (parts) setText(displayText(formatValue(parts, time), time)); }}
        onKeyDown={onInputKeyDown}
      />
      <button
        type="button"
        className="icon-btn icon-btn-xs date-input-btn"
        popoverTarget={panelId}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={t(time ? "date.chooseTime" : "date.choose")}
        title={t(time ? "date.chooseTime" : "date.choose")}
        disabled={disabled}
      >
        <RiCalendarLine size={16} aria-hidden />
      </button>
      <div ref={panel} id={panelId} popover="auto" role="dialog" aria-label={t(time ? "date.chooseTime" : "date.choose")} className="popover date-picker" onBeforeToggle={onBeforeToggle} onToggle={onToggle}>
        {open && <DatePanel value={parseDateTime(text)} time={time} utc={utc} onChoose={choose} onClose={close} />}
      </div>
    </div>
  );
}

interface DatePanelProps {
  value: Parts | null;
  time: boolean;
  utc: boolean;
  onChoose: (parts: Parts | null) => void;
  onClose: () => void;
}

/** Month grid (arrow keys, Page Up/Down, Home/End), the time of day, and Now / Clear. Rendered only while open. */
export function DatePanel({ value, time, utc, onChoose, onClose }: DatePanelProps) {
  const t = useT();
  const language = useLanguage();
  const today = now(utc);
  // The day the keyboard is on; the shown month follows it.
  const [cursor, setCursor] = useState(() => (value ? { year: value.year, month: value.month, day: value.day } : { year: today.year, month: today.month, day: today.day }));
  const grid = useRef<HTMLTableElement>(null);
  const moved = useRef(false);
  const labelId = useId();

  const names = useMemo(() => {
    const month = new Intl.DateTimeFormat(language, { year: "numeric", month: "long", timeZone: "UTC" });
    const full = new Intl.DateTimeFormat(language, { dateStyle: "full", timeZone: "UTC" });
    const weekdayShort = new Intl.DateTimeFormat(language, { weekday: "short", timeZone: "UTC" });
    const weekdayLong = new Intl.DateTimeFormat(language, { weekday: "long", timeZone: "UTC" });
    // 2023-01-01 was a Sunday; both UI languages start the week on Sunday.
    const weekdays = Array.from({ length: 7 }, (_, index) => ({ short: weekdayShort.format(Date.UTC(2023, 0, 1 + index)), long: weekdayLong.format(Date.UTC(2023, 0, 1 + index)) }));
    return { month, full, weekdays };
  }, [language]);

  // Focus lands on the cursor day when the panel opens and after each keyboard move.
  useEffect(() => {
    grid.current?.querySelector<HTMLButtonElement>("button[tabindex='0']")?.focus();
  }, []);
  useEffect(() => {
    if (!moved.current) return;
    moved.current = false;
    grid.current?.querySelector<HTMLButtonElement>("button[tabindex='0']")?.focus();
  }, [cursor]);

  const move = (year: number, month: number, day: number, focus = true) => {
    // Normalised through Date.UTC, so day 0 or 32 rolls into the neighbouring month.
    const date = new Date(Date.UTC(year, month - 1, day));
    moved.current = focus;
    setCursor({ year: date.getUTCFullYear(), month: date.getUTCMonth() + 1, day: date.getUTCDate() });
  };
  const shiftMonth = (by: number, focus = false) => {
    const target = new Date(Date.UTC(cursor.year, cursor.month - 1 + by, 1));
    const year = target.getUTCFullYear();
    const month = target.getUTCMonth() + 1;
    move(year, month, Math.min(cursor.day, daysIn(year, month)), focus);
  };
  const base = value ?? { ...today, hour: 0, minute: 0, second: 0, fraction: "" };
  const pick = (year: number, month: number, day: number) => {
    onChoose({ ...base, year, month, day });
    if (!time) onClose();
  };

  const onGridKeyDown = (event: KeyboardEvent) => {
    const weekday = new Date(Date.UTC(cursor.year, cursor.month - 1, cursor.day)).getUTCDay();
    const steps: Record<string, () => void> = {
      ArrowLeft: () => move(cursor.year, cursor.month, cursor.day - 1),
      ArrowRight: () => move(cursor.year, cursor.month, cursor.day + 1),
      ArrowUp: () => move(cursor.year, cursor.month, cursor.day - 7),
      ArrowDown: () => move(cursor.year, cursor.month, cursor.day + 7),
      Home: () => move(cursor.year, cursor.month, cursor.day - weekday),
      End: () => move(cursor.year, cursor.month, cursor.day + 6 - weekday),
      PageUp: () => shiftMonth(event.shiftKey ? -12 : -1, true),
      PageDown: () => shiftMonth(event.shiftKey ? 12 : 1, true),
    };
    const step = steps[event.key];
    if (!step) return;
    event.preventDefault();
    step();
  };

  // Six rows from the Sunday on or before the first of the month, so the grid never changes height.
  const first = new Date(Date.UTC(cursor.year, cursor.month - 1, 1));
  const start = Date.UTC(cursor.year, cursor.month - 1, 1 - first.getUTCDay());
  const weeks = Array.from({ length: 6 }, (_, week) => Array.from({ length: 7 }, (_, day) => new Date(start + (week * 7 + day) * 86_400_000)));
  const same = (date: Date, parts: { year: number; month: number; day: number } | null) =>
    parts !== null && date.getUTCFullYear() === parts.year && date.getUTCMonth() + 1 === parts.month && date.getUTCDate() === parts.day;

  const setTime = (patch: Partial<Parts>) => onChoose({ ...base, ...(value ? {} : { year: cursor.year, month: cursor.month, day: cursor.day }), ...patch, fraction: "" });

  return (
    <div className="date-panel">
      <div className="date-head">
        <IconButton size="xs" label={t("date.previousMonth")} icon={RiArrowLeftSLine} onClick={() => shiftMonth(-1)} />
        <span id={labelId} className="date-month" aria-live="polite">{names.month.format(first)}</span>
        <IconButton size="xs" label={t("date.nextMonth")} icon={RiArrowRightSLine} onClick={() => shiftMonth(1)} />
      </div>
      <table ref={grid} className="date-grid" role="grid" aria-labelledby={labelId} onKeyDown={onGridKeyDown}>
        <thead>
          <tr>
            {names.weekdays.map((weekday) => (
              <th key={weekday.long} scope="col" abbr={weekday.long}>{weekday.short}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {weeks.map((week) => (
            <tr key={week[0].getTime()}>
              {week.map((date) => {
                const current = same(date, cursor);
                const selected = same(date, value);
                return (
                  <td key={date.getTime()} role="gridcell" aria-selected={selected}>
                    <button
                      type="button"
                      className="date-day"
                      tabIndex={current ? 0 : -1}
                      aria-label={names.full.format(date)}
                      aria-current={same(date, today) ? "date" : undefined}
                      data-outside={date.getUTCMonth() + 1 !== cursor.month ? "" : undefined}
                      data-selected={selected ? "" : undefined}
                      onClick={() => {
                        move(date.getUTCFullYear(), date.getUTCMonth() + 1, date.getUTCDate(), false);
                        pick(date.getUTCFullYear(), date.getUTCMonth() + 1, date.getUTCDate());
                      }}
                    >
                      {date.getUTCDate()}
                    </button>
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
      {time && (
        <div className="date-time" role="group" aria-label={t("date.time")}>
          <TimeUnit label={t("date.hour")} value={base.hour} max={23} onChange={(hour) => setTime({ hour })} onDone={onClose} />
          <span aria-hidden>:</span>
          <TimeUnit label={t("date.minute")} value={base.minute} max={59} onChange={(minute) => setTime({ minute })} onDone={onClose} />
          <span aria-hidden>:</span>
          <TimeUnit label={t("date.second")} value={base.second} max={59} onChange={(second) => setTime({ second })} onDone={onClose} />
          {utc && <span className="date-zone">UTC</span>}
        </div>
      )}
      <div className="date-foot">
        <Button size="sm" variant="transparent" onClick={() => {
          const current = now(utc);
          setCursor({ year: current.year, month: current.month, day: current.day });
          onChoose(time ? current : { ...current, hour: 0, minute: 0, second: 0 });
          if (!time) onClose();
        }}>{t(time ? "date.now" : "date.today")}</Button>
        <Button size="sm" variant="transparent" onClick={() => {
          onChoose(null);
          onClose();
        }}>{t("date.clear")}</Button>
        {time && <Button size="sm" variant="secondary" className="date-done" onClick={onClose}>{t("date.done")}</Button>}
      </div>
    </div>
  );
}

/**
 * Two digits of the time: typed, or stepped with the arrow keys (wrapping), committed as soon as they are in range.
 * Enter closes the calendar instead of submitting the surrounding form.
 */
function TimeUnit({ label, value, max, onChange, onDone }: { label: string; value: number; max: number; onChange: (value: number) => void; onDone: () => void }) {
  const [text, setText] = useState(pad(value));
  // A typed "1" already means 1, so committing it does not rewrite the box under the caret.
  useEffect(() => setText((current) => (current !== "" && Number(current) === value ? current : pad(value))), [value]);
  return (
    <input
      className="field-control control-sm mono date-unit"
      type="text"
      inputMode="numeric"
      maxLength={2}
      aria-label={label}
      value={text}
      onFocus={(event) => event.target.select()}
      onChange={(event) => {
        const digits = event.target.value.replace(/\D/g, "").slice(0, 2);
        setText(digits);
        if (digits !== "" && Number(digits) <= max) onChange(Number(digits));
      }}
      onBlur={() => setText(pad(value))}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.preventDefault();
          onDone();
          return;
        }
        if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
        event.preventDefault();
        onChange((value + (event.key === "ArrowUp" ? 1 : max)) % (max + 1));
      }}
    />
  );
}
