import type { ReactNode } from "react";
import "../styles/kit.css";

export interface Fact {
  label: ReactNode;
  value: ReactNode;
  mono?: boolean;
}

/** Read-only key/value rows for a detail pane; falsy entries are skipped. */
export function Facts({ items }: { items: (Fact | false | null | undefined)[] }) {
  return (
    <dl className="facts">
      {items.filter((item): item is Fact => Boolean(item)).map((item, index) => (
        <div key={index}>
          <dt>{item.label}</dt>
          <dd className={item.mono ? "mono" : undefined}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}
