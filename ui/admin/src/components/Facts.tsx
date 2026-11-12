import type { ReactNode } from "react";
import { CopyButton } from "./CopyButton";

export interface Fact {
  label: ReactNode;
  /** null or undefined shows a muted dash. */
  value: ReactNode;
  mono?: boolean;
  tone?: "danger" | "warning" | "success";
  /** Adds the shared copy button for a string value (ids, URLs). */
  copy?: boolean;
}

/** Read-only key/value rows for a detail pane; falsy entries are skipped. */
export function Facts({ items }: { items: (Fact | false | null | undefined)[] }) {
  return (
    <dl className="facts">
      {items.filter((item): item is Fact => Boolean(item)).map((item, index) => (
        <div key={index}>
          <dt>{item.label}</dt>
          <dd className={item.mono ? "mono" : undefined} data-tone={item.tone}>
            {item.value ?? <span className="na">–</span>}
            {item.copy && typeof item.value === "string" && <CopyButton className="facts-copy" value={item.value} />}
          </dd>
        </div>
      ))}
    </dl>
  );
}
