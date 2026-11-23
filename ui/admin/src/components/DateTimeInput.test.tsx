import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setLanguage } from "../i18n";
import { DatePanel, DateTimeInput, displayText, formatValue, parseDateTime } from "./DateTimeInput";

beforeEach(() => setLanguage("en"));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

describe("date text", () => {
  it("reads typed and stored forms into one normalised value", () => {
    const read = (text: string, time = true) => {
      const parts = parseDateTime(text);
      return parts && formatValue(parts, time);
    };
    expect(read("2026-11-24")).toBe("2026-11-24T00:00:00");
    expect(read("2026-1-5 7:03")).toBe("2026-01-05T07:03:00");
    expect(read("2026. 11. 24. 09:05:07")).toBe("2026-11-24T09:05:07");
    expect(read("2026/11/24T09:05:07.123456")).toBe("2026-11-24T09:05:07.123456");
    expect(read("2026-11-24 09:05", false)).toBe("2026-11-24");
    // Out of range days and times are rejected rather than rolled over.
    for (const text of ["2026-02-29", "2026-13-01", "2026-11-24 24:00", "2026-11-24 10:60", "24/11/2026", "soon"]) expect(read(text), text).toBeNull();
    expect(read("2028-02-29")).toBe("2028-02-29T00:00:00");
  });

  it("shows a value with a space for the T and leaves text that does not parse alone", () => {
    expect(displayText("2026-11-24T09:05:00", true)).toBe("2026-11-24 09:05:00");
    expect(displayText("2026-11-24T09:05:00", false)).toBe("2026-11-24");
    expect(displayText("2026-11-24T09:05", true)).toBe("2026-11-24 09:05:00");
    expect(displayText("tomorrow", true)).toBe("tomorrow");
  });
});

function Controlled({ initial = "", onChange, time }: { initial?: string; onChange: (value: string) => void; time?: boolean }) {
  const [value, setValue] = useState(initial);
  return <DateTimeInput aria-label="When" value={value} time={time} onChange={(next) => { setValue(next); onChange(next); }} />;
}

describe("DateTimeInput", () => {
  it("types in the fixed form and reports the normalised value", () => {
    const onChange = vi.fn();
    render(<Controlled initial="2026-11-24T09:05:00" onChange={onChange} />);
    const box = screen.getByRole("textbox", { name: "When" });
    expect(box).toHaveValue("2026-11-24 09:05:00");
    fireEvent.change(box, { target: { value: "2026-1-5 7:03" } });
    expect(onChange).toHaveBeenLastCalledWith("2026-01-05T07:03:00");
    // The text being typed stays as typed until the box is left.
    expect(box).toHaveValue("2026-1-5 7:03");
    fireEvent.blur(box);
    expect(box).toHaveValue("2026-01-05 07:03:00");
  });

  it("marks text that is not a date and hands it on as typed", () => {
    const onChange = vi.fn();
    render(<Controlled onChange={onChange} />);
    const box = screen.getByRole("textbox", { name: "When" });
    fireEvent.change(box, { target: { value: "2026-02-30" } });
    expect(onChange).toHaveBeenLastCalledWith("2026-02-30");
    expect(box).toHaveAttribute("aria-invalid", "true");
    expect(box).toHaveAttribute("title", "Enter a date and time as YYYY-MM-DD HH:mm:ss.");
    fireEvent.change(box, { target: { value: "" } });
    expect(onChange).toHaveBeenLastCalledWith("");
    expect(box).not.toHaveAttribute("aria-invalid");
  });

  it("keeps a half-typed value when the owner ignores it, and follows a new value from outside", () => {
    function Ignoring() {
      const [value, setValue] = useState("2026-11-24T09:05:00");
      return (
        <>
          <DateTimeInput aria-label="When" value={value} onChange={(next) => { if (parseDateTime(next)) setValue(next); }} />
          <button type="button" onClick={() => setValue("2027-01-02T03:04:05")}>Reset</button>
        </>
      );
    }
    render(<Ignoring />);
    const box = screen.getByRole("textbox", { name: "When" });
    fireEvent.change(box, { target: { value: "2026-11-2" } });
    expect(box).toHaveValue("2026-11-2");
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));
    expect(box).toHaveValue("2027-01-02 03:04:05");
  });

  it("names its calendar button by mode", () => {
    render(<DateTimeInput aria-label="Day" value="" time={false} onChange={() => {}} />);
    expect(screen.getByRole("button", { name: "Choose date" })).toHaveAttribute("aria-haspopup", "dialog");
    expect(screen.getByRole("textbox", { name: "Day" })).toHaveAttribute("placeholder", "YYYY-MM-DD");
  });
});

describe("DatePanel", () => {
  const value = parseDateTime("2026-11-24 09:05:07")!;

  it("labels the month and weekdays in the UI language", () => {
    render(<DatePanel value={value} time utc={false} onChoose={() => {}} onClose={() => {}} />);
    expect(screen.getByRole("grid", { name: "November 2026" })).toBeInTheDocument();
    expect(screen.getAllByRole("columnheader").map((cell) => cell.textContent)).toEqual(["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]);
    cleanup();
    act(() => void setLanguage("ko"));
    render(<DatePanel value={value} time utc={false} onChoose={() => {}} onClose={() => {}} />);
    expect(screen.getByRole("grid", { name: "2026년 11월" })).toBeInTheDocument();
    expect(screen.getAllByRole("columnheader")[0]).toHaveTextContent("일");
    expect(screen.getByRole("button", { name: "이전 달" })).toBeInTheDocument();
  });

  it("focuses the chosen day and moves by day, week and month from the keyboard", () => {
    render(<DatePanel value={value} time utc={false} onChoose={() => {}} onClose={() => {}} />);
    const selected = screen.getByRole("button", { name: "Tuesday, November 24, 2026" });
    expect(selected).toHaveFocus();
    expect(selected.closest("td")).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(selected, { key: "ArrowRight" });
    expect(screen.getByRole("button", { name: "Wednesday, November 25, 2026" })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: "ArrowDown" });
    expect(screen.getByRole("button", { name: "Wednesday, December 2, 2026" })).toHaveFocus();
    expect(screen.getByRole("grid", { name: "December 2026" })).toBeInTheDocument();
    fireEvent.keyDown(document.activeElement!, { key: "PageUp", shiftKey: true });
    expect(screen.getByRole("button", { name: "Tuesday, December 2, 2025" })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: "Home" });
    expect(screen.getByRole("button", { name: "Sunday, November 30, 2025" })).toHaveFocus();
    fireEvent.keyDown(document.activeElement!, { key: "End" });
    expect(screen.getByRole("button", { name: "Saturday, December 6, 2025" })).toHaveFocus();
    // Only the focused day is in the tab order.
    expect(screen.getAllByRole("gridcell").filter((cell) => cell.querySelector("button")?.tabIndex === 0)).toHaveLength(1);
  });

  it("keeps the time when a day is picked and edits the time of day", () => {
    const onChoose = vi.fn();
    const onClose = vi.fn();
    render(<DatePanel value={value} time utc={false} onChoose={onChoose} onClose={onClose} />);
    fireEvent.click(screen.getByRole("button", { name: "Monday, November 2, 2026" }));
    expect(formatValue(onChoose.mock.lastCall![0], true)).toBe("2026-11-02T09:05:07");
    expect(onClose).not.toHaveBeenCalled();
    const hour = screen.getByRole("textbox", { name: "Hour" });
    fireEvent.change(hour, { target: { value: "1" } });
    expect(formatValue(onChoose.mock.lastCall![0], true)).toBe("2026-11-24T01:05:07");
    fireEvent.change(hour, { target: { value: "17" } });
    expect(formatValue(onChoose.mock.lastCall![0], true)).toBe("2026-11-24T17:05:07");
    fireEvent.change(hour, { target: { value: "99" } });
    expect(onChoose).toHaveBeenCalledTimes(3);
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Minute" }), { key: "ArrowDown" });
    expect(formatValue(onChoose.mock.lastCall![0], true)).toBe("2026-11-24T09:04:07");
    fireEvent.keyDown(screen.getByRole("textbox", { name: "Second" }), { key: "Enter" });
    expect(onClose).toHaveBeenCalled();
  });

  it("picks a date and closes in date-only mode; Today and Clear", () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date(2026, 10, 24, 15, 30, 0));
    const onChoose = vi.fn();
    const onClose = vi.fn();
    render(<DatePanel value={null} time={false} utc={false} onChoose={onChoose} onClose={onClose} />);
    expect(screen.getByRole("button", { name: "Tuesday, November 24, 2026" })).toHaveAttribute("aria-current", "date");
    expect(screen.queryByRole("group", { name: "Time" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Friday, November 27, 2026" }));
    expect(formatValue(onChoose.mock.lastCall![0], false)).toBe("2026-11-27");
    expect(onClose).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Today" }));
    expect(formatValue(onChoose.mock.lastCall![0], false)).toBe("2026-11-24");
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onChoose).toHaveBeenLastCalledWith(null);
  });
});
