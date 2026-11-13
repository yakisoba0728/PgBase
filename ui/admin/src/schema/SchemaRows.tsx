import type { ReactNode } from "react";
import {
  RiAddLine,
  RiArrowDownSLine,
  RiArrowUpSLine,
  RiAttachment2,
  RiBracesLine,
  RiCalendarLine,
  RiFileTextLine,
  RiHashtag,
  RiKey2Line,
  RiLinksLine,
  RiLockLine,
  RiLockPasswordLine,
  RiMapPinLine,
  RiNumbersLine,
  RiText,
  RiTimeLine,
  RiToggleLine,
  type RemixiconComponentType,
} from "@remixicon/react";
import type { CollectionField, FieldType, IndexKey } from "../api/collections";
import { IconButton } from "../components/Button";
import { Dropdown, DropdownItem } from "../components/Popover";
import { ApiError } from "../api/http";
import { en } from "../i18n/en";
import { useT, type MessageKey, type Translate } from "../i18n";
import { fieldTypes, rowState, type DraftField, type DraftIndex } from "./draft";
import type { SchemaEditor } from "./useSchemaEditor";

const typeIcons: Record<FieldType, RemixiconComponentType> = {
  text: RiText,
  editor: RiFileTextLine,
  integer: RiHashtag,
  bigint: RiHashtag,
  numeric: RiNumbersLine,
  boolean: RiToggleLine,
  timestamptz: RiCalendarLine,
  autodate: RiTimeLine,
  jsonb: RiBracesLine,
  relation: RiLinksLine,
  file: RiAttachment2,
  uuid: RiKey2Line,
  geoPoint: RiMapPinLine,
  password: RiLockPasswordLine,
};

/** "options: invalid_options" -> "options: Options are not valid for this type." */
export function describeErrors(t: Translate, entries: string[]): string {
  return entries
    .map((entry) => {
      const split = entry.lastIndexOf(": ");
      const [prefix, code] = split < 0 ? ["", entry] : [entry.slice(0, split + 2), entry.slice(split + 2)];
      const key = `schema.error.${code}`;
      return prefix + (key in en ? t(key as MessageKey) : code.replaceAll("_", " "));
    })
    .join(" · ");
}

/** An `invalid_view_sql` rejection: the server's reason, then PostgreSQL's own message and 1-based position when analysis failed. */
export function viewSqlProblem(t: Translate, error: unknown): string | undefined {
  if (!(error instanceof ApiError) || error.code !== "invalid_view_sql") return undefined;
  const details = (error.details ?? {}) as Record<string, string[] | undefined>;
  const first = (suffix: string) => details[`view.sql${suffix}`]?.[0];
  const reason = first("") ?? "invalid";
  const key = `schema.viewSql.${reason}`;
  const position = first(".position");
  return [key in en ? t(key as MessageKey) : reason.replaceAll("_", " "), first(".message"), position && t("schema.viewSql.position", { position }), first(".column") ?? first(".relation")]
    .filter(Boolean)
    .join(" — ");
}

const flagNames = ["required", "unique", "indexed", "hidden", "presentable"] as const;

export function Section({ title, count, action, children }: { title: string; count?: number; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="schema-section" aria-label={title}>
      <header className="schema-section-head">
        <h2>{title}</h2>
        {count !== undefined && <span className="schema-count">{count}</span>}
        {action && <div className="schema-section-action">{action}</div>}
      </header>
      {children}
    </section>
  );
}

function Flags({ field }: { field: Pick<DraftField, (typeof flagNames)[number]> }) {
  const t = useT();
  return (
    <span className="schema-flags">
      {flagNames.filter((flag) => field[flag]).map((flag) => (
        <span key={flag} className="schema-flag">{t(`schema.flag.${flag}`)}</span>
      ))}
    </span>
  );
}

/** Columns every table has; shown for orientation, never editable. */
function LockedRow({ name, type }: { name: string; type: FieldType }) {
  const t = useT();
  const Icon = typeIcons[type];
  return (
    <li className="schema-row" data-locked="">
      <div className="schema-row-main">
        <Icon size={16} aria-hidden />
        <span className="schema-name">{name}</span>
        <span className="schema-type">{type}</span>
        <span className="schema-flags" />
        <RiLockLine className="schema-lock" size={14} aria-label={t("schema.locked")} />
      </div>
      <span className="schema-row-tools" />
    </li>
  );
}

export function FieldRows({ editor }: { editor: SchemaEditor }) {
  const t = useT();
  const { draft, base, errors } = editor;
  const add = (
    <Dropdown label={t("schema.addField")} text={t("schema.addField")} icon={RiAddLine} heading={t("schema.fieldType")}>
      {fieldTypes.map((type) => (
        <DropdownItem key={type} icon={typeIcons[type]} onSelect={() => editor.addField(type)}>{t(`schema.type.${type}`)}</DropdownItem>
      ))}
    </Dropdown>
  );
  return (
    <Section title={t("schema.fields")} count={draft.fields.length} action={add}>
      <ul className="schema-rows">
        <LockedRow name="id" type={draft.recordId.kind === "text" ? "text" : "uuid"} />
        {draft.fields.map((field, position) => {
          const Icon = typeIcons[field.type];
          const fieldErrors = errors.fields[field.key];
          const state = rowState(field, base.fields);
          const locked = Boolean(field.system);
          return (
            <li key={field.key} className="schema-row" data-state={state} data-invalid={fieldErrors ? "" : undefined} aria-current={editor.openField?.key === field.key || undefined} data-locked={locked ? "" : undefined}>
              <button type="button" className="schema-row-main" onClick={() => editor.setPane({ field: field.key })}>
                <Icon size={16} aria-hidden />
                <span className="schema-name">{field.name || <span className="na">{t("schema.unnamed")}</span>}</span>
                <span className="schema-type">{field.type === "relation" && field.relation?.targetCollection ? `→ ${field.relation.targetCollection}` : field.type}</span>
                <Flags field={field} />
                {state && <span className="schema-state">{t(`schema.state.${state}`)}</span>}
                {locked && <RiLockLine className="schema-lock" size={14} aria-label={t("schema.locked")} />}
              </button>
              <span className="schema-row-tools">
                <IconButton label={t("schema.moveUp", { name: field.name })} icon={RiArrowUpSLine} disabled={position === 0} onClick={() => editor.moveField(field.key, -1)} />
                <IconButton label={t("schema.moveDown", { name: field.name })} icon={RiArrowDownSLine} disabled={position === draft.fields.length - 1} onClick={() => editor.moveField(field.key, 1)} />
              </span>
              {fieldErrors && <p className="schema-row-error" role="alert">{describeErrors(t, fieldErrors)}</p>}
            </li>
          );
        })}
        <LockedRow name="created_at" type="autodate" />
        <LockedRow name="updated_at" type="autodate" />
      </ul>
    </Section>
  );
}

/** Read-only field list of a view: its columns follow the query. */
export function ViewFieldRows({ fields, textId = false }: { fields: CollectionField[]; textId?: boolean }) {
  const t = useT();
  return (
    <Section title={t("schema.fields")} count={fields.length}>
      <ul className="schema-rows">
        <LockedRow name="id" type={textId ? "text" : "uuid"} />
        {fields.map((field) => <LockedRow key={field.id} name={field.name} type={field.type} />)}
      </ul>
      <p className="field-hint schema-note">{t("schema.viewFieldsHint")}</p>
    </Section>
  );
}

export function indexColumns(index: Pick<DraftIndex, "keys" | "fields">): IndexKey[] {
  return index.keys?.length ? index.keys : (index.fields ?? []).map((field) => ({ field }));
}

export function IndexRows({ editor }: { editor: SchemaEditor }) {
  const t = useT();
  const { draft, base, errors } = editor;
  const add = <IconButton label={t("schema.addIndex")} icon={RiAddLine} onClick={editor.addIndex} />;
  return (
    <Section title={t("schema.indexes")} count={draft.indexes.length} action={add}>
      {draft.indexes.length === 0 ? (
        <p className="field-hint schema-note">{t("schema.noIndexes")}</p>
      ) : (
        <ul className="schema-rows">
          {draft.indexes.map((index) => {
            const indexErrors = errors.indexes[index.key];
            const state = rowState(index, base.indexes);
            const columns = index.rawSql
              ? index.rawSql
              : indexColumns(index).map((key) => `${key.field ?? key.expression}${key.order === "desc" ? " desc" : ""}`).join(", ");
            return (
              <li key={index.key} className="schema-row" data-state={state} data-invalid={indexErrors ? "" : undefined} aria-current={editor.openIndex?.key === index.key || undefined}>
                <button type="button" className="schema-row-main" onClick={() => editor.setPane({ index: index.key })}>
                  <RiKey2Line size={16} aria-hidden />
                  <span className="schema-name">{index.name}</span>
                  <span className="schema-type schema-columns">{columns || <span className="na">{t("schema.noColumns")}</span>}{index.where ? ` where ${index.where}` : ""}</span>
                  <span className="schema-flags">{index.unique && <span className="schema-flag">{t("schema.flag.unique")}</span>}</span>
                  {state && <span className="schema-state">{t(`schema.state.${state}`)}</span>}
                </button>
                {indexErrors && <p className="schema-row-error" role="alert">{describeErrors(t, indexErrors)}</p>}
              </li>
            );
          })}
        </ul>
      )}
    </Section>
  );
}
