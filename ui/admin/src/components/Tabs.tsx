import { useLayoutEffect, useRef, type KeyboardEvent, type RefObject } from "react";

/**
 * The tab rows' moving underline: measures the current tab (aria-selected or aria-current) into --tab-x/--tab-w on
 * the row, which draws one indicator that slides between tabs. Without it (no observers) each tab keeps its own underline.
 * The navigation uses the same along the other axis ("y": --tab-y/--tab-h) for its current-item highlight.
 */
export function useTabIndicator(ref: RefObject<HTMLElement | null>, shown = true, axis: "x" | "y" = "x") {
  useLayoutEffect(() => {
    const row = ref.current;
    if (!row || typeof ResizeObserver === "undefined") return;
    const measure = () => {
      // The first current item that is laid out (the navigation hides rail-only twins).
      const current = [...row.querySelectorAll<HTMLElement>('[aria-selected="true"], [aria-current="page"]')].find((item) => item.offsetWidth > 0);
      if (!current) return row.removeAttribute("data-indicator");
      if (axis === "x") {
        row.style.setProperty("--tab-x", `${current.offsetLeft}px`);
        row.style.setProperty("--tab-w", `${current.offsetWidth}px`);
      } else {
        row.style.setProperty("--tab-y", `${current.offsetTop}px`);
        row.style.setProperty("--tab-h", `${current.offsetHeight}px`);
      }
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
  }, [ref, shown, axis]);
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
