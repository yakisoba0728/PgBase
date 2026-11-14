import { useLayoutEffect, useRef, type KeyboardEvent, type RefObject } from "react";

/**
 * The tab rows' moving underline: measures the current tab (aria-selected or aria-current) into --tab-x/--tab-w on
 * the row, which draws one indicator that slides between tabs. Without it (no observers) each tab keeps its own underline.
 */
export function useTabIndicator(ref: RefObject<HTMLElement | null>, shown = true) {
  useLayoutEffect(() => {
    const row = ref.current;
    if (!row || typeof ResizeObserver === "undefined") return;
    const measure = () => {
      const current = row.querySelector<HTMLElement>('[aria-selected="true"], [aria-current="page"]');
      if (!current) return row.removeAttribute("data-indicator");
      row.style.setProperty("--tab-x", `${current.offsetLeft}px`);
      row.style.setProperty("--tab-w", `${current.offsetWidth}px`);
      row.setAttribute("data-indicator", "");
      // The current tab's own width changes too (language switch, webfont arriving).
      resized.observe(current);
    };
    const resized = new ResizeObserver(measure);
    resized.observe(row);
    measure();
    const changed = new MutationObserver(measure);
    changed.observe(row, { subtree: true, childList: true, attributeFilter: ["aria-selected", "aria-current"] });
    return () => {
      resized.disconnect();
      changed.disconnect();
    };
  }, [ref, shown]);
}

interface TabsProps<T extends string> {
  label: string;
  tabs: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
}

export function Tabs<T extends string>({ label, tabs, value, onChange }: TabsProps<T>) {
  const row = useRef<HTMLDivElement>(null);
  useTabIndicator(row);
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    event.preventDefault();
    const index = tabs.findIndex((tab) => tab.value === value);
    const next = (index + step + tabs.length) % tabs.length;
    onChange(tabs[next].value);
    event.currentTarget.querySelectorAll<HTMLButtonElement>("[role=tab]")[next]?.focus();
  };
  return (
    <div ref={row} className="tabs" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
      {tabs.map((tab) => (
        <button
          key={tab.value}
          type="button"
          role="tab"
          className="tab"
          aria-selected={tab.value === value}
          tabIndex={tab.value === value ? 0 : -1}
          onClick={() => onChange(tab.value)}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}
