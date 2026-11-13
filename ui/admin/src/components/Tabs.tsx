import type { KeyboardEvent } from "react";

interface TabsProps<T extends string> {
  label: string;
  tabs: { value: T; label: string }[];
  value: T;
  onChange: (value: T) => void;
}

export function Tabs<T extends string>({ label, tabs, value, onChange }: TabsProps<T>) {
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
    <div className="tabs" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
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
