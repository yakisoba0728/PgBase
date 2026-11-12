import { RiAlertLine } from "@remixicon/react";
import { useT } from "../i18n";
import { CodeBlock } from "./CodeBlock";

export interface Change {
  kind: "add" | "remove" | "rename" | "change" | "move";
  target: "collection" | "field" | "index";
  name: string;
  /** Previous name for a rename; changed properties for a change. */
  detail?: string;
  /** Loses data (or may). */
  danger: boolean;
}

interface ChangeListProps {
  title?: string;
  changes: Change[];
  sql: string[];
  /** Marks statements that destroy data; their SQL opens by default. */
  dangerous: (statement: string) => boolean;
  /** Shown when the plan changes no table (metadata only). */
  emptyText?: string;
}

/** A plan as a reviewer reads it: what changes, data-loss marks, then the SQL it runs. */
export function ChangeList({ title, changes, sql, dangerous, emptyText }: ChangeListProps) {
  const t = useT();
  const risky = sql.some(dangerous);
  return (
    <section className="schema-plan">
      {title && <h3 className="schema-plan-title mono">{title}</h3>}
      {changes.length > 0 ? (
        <ul className="schema-changes">
          {changes.map((item, position) => (
            <li key={position} data-kind={item.kind} data-danger={item.danger ? "" : undefined}>
              <span className="schema-change-kind">{t(`schema.kind.${item.kind}`)}</span>
              <span className="schema-change-target">{t(`schema.target.${item.target}`)}</span>
              <span className="mono">{item.kind === "rename" ? `${item.detail} → ${item.name}` : item.name}</span>
              {item.kind !== "rename" && item.detail && <span className="schema-change-detail mono">{item.detail}</span>}
              {item.danger && <RiAlertLine size={14} aria-label={t("schema.dataLoss")} />}
            </li>
          ))}
        </ul>
      ) : (
        emptyText && <p className="field-hint">{emptyText}</p>
      )}
      {sql.length > 0 && (
        <details className="schema-sql" open={risky || changes.some((item) => item.danger)}>
          <summary>{t("schema.sql", { count: sql.length })}</summary>
          <CodeBlock wrap label={t("schema.sql", { count: sql.length })} text={sql.map((statement) => ({ text: statement, danger: dangerous(statement) }))} />
        </details>
      )}
    </section>
  );
}
