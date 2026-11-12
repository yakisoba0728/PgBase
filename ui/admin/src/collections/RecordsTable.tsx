import type { KeyboardEvent, MouseEvent, ReactNode } from "react";
import { RiArrowDownLine, RiArrowUpLine, RiAttachment2, RiFileCopyLine } from "@remixicon/react";
import type { CollectionField } from "../api/collections";
import type { RecordData } from "../api/records";
import { IconButton } from "../components/Button";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { cellView, fieldKind, shortDate } from "./fields";

interface RecordsTableProps {
  label: string;
  fields: CollectionField[];
  records: RecordData[];
  sort: string;
  onSort: (field: string) => void;
  openId: string | null;
  onOpen: (id: string) => void;
  selected: Set<string>;
  onSelect: (ids: string[], checked: boolean) => void;
  /** SQL views may sort on unindexed columns. */
  sqlView: boolean;
}

// The server sorts only on indexed or unique columns (else 422 sort: unindexed_field), except
// in SQL views, and never on json, file or password fields; of the system columns, id and created_at.
const sortableType = (field: CollectionField) => !["file", "relations", "json", "multiselect", "password"].includes(fieldKind(field));
const sortableSystem = ["id", "created_at"];

export function RecordsTable({ label, fields, records, sort, onSort, openId, onOpen, selected, onSelect, sqlView }: RecordsTableProps) {
  const t = useT();
  const columns = fields.filter((field) => field.type !== "password");
  const system = ["created_at", "updated_at"].filter((name) => !fields.some((field) => field.name === name));
  const checkedCount = records.filter((record) => selected.has(record.id)).length;
  const allIds = records.map((record) => record.id);

  const header = (name: string, children: ReactNode, canSort = true) => {
    const direction = sort === name ? "asc" : sort === `-${name}` ? "desc" : undefined;
    if (!canSort) return children;
    return (
      <button type="button" className="sort-btn" data-sorted={direction ? "" : undefined} onClick={() => onSort(name)}>
        {children}
        {direction === "asc" && <RiArrowUpLine size={13} aria-label={t("records.sortedAsc")} />}
        {direction === "desc" && <RiArrowDownLine size={13} aria-label={t("records.sortedDesc")} />}
      </button>
    );
  };
  const ariaSort = (name: string) => (sort === name ? "ascending" : sort === `-${name}` ? "descending" : undefined);

  return (
    <table className="grid" aria-label={label}>
      <thead>
        <tr>
          <th className="col-check">
            <input
              type="checkbox"
              aria-label={t("records.selectAll")}
              checked={records.length > 0 && checkedCount === records.length}
              ref={(input) => {
                if (input) input.indeterminate = checkedCount > 0 && checkedCount < records.length;
              }}
              onChange={(event) => onSelect(allIds, event.target.checked)}
            />
          </th>
          <th aria-sort={ariaSort("id")}>{header("id", "id")}</th>
          {columns.map((field) => (
            <th key={field.id} className={fieldKind(field) === "number" ? "num" : undefined} aria-sort={ariaSort(field.name)}>
              {header(
                field.name,
                <>
                  {field.name} <span className="th-type">{field.type}</span>
                </>,
                sortableType(field) && (sqlView || field.indexed || field.unique),
              )}
            </th>
          ))}
          {system.map((name) => (
            <th key={name} aria-sort={ariaSort(name)}>{header(name, name, sqlView || sortableSystem.includes(name))}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {records.map((record) => (
          <RecordRow key={record.id} record={record} columns={columns} system={system} current={record.id === openId} checked={selected.has(record.id)} onOpen={onOpen} onSelect={onSelect} />
        ))}
      </tbody>
    </table>
  );
}

interface RecordRowProps {
  record: RecordData;
  columns: CollectionField[];
  system: string[];
  current: boolean;
  checked: boolean;
  onOpen: (id: string) => void;
  onSelect: (ids: string[], checked: boolean) => void;
}

function RecordRow({ record, columns, system, current, checked, onOpen, onSelect }: RecordRowProps) {
  const t = useT();
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.target === event.currentTarget && (event.key === "Enter" || event.key === " ")) {
      event.preventDefault();
      onOpen(record.id);
    }
  };
  const stop = (event: MouseEvent) => event.stopPropagation();
  const copy = (event: MouseEvent) => {
    event.stopPropagation();
    navigator.clipboard.writeText(record.id).then(
      () => toast(t("records.copied")),
      () => toast(t("records.copyFailed"), "danger"),
    );
  };
  return (
    <tr tabIndex={0} aria-current={current || undefined} data-checked={checked ? "" : undefined} onClick={() => onOpen(record.id)} onKeyDown={onKeyDown}>
      <td className="col-check" onClick={stop}>
        <input type="checkbox" aria-label={t("records.select", { id: record.id })} checked={checked} onChange={(event) => onSelect([record.id], event.target.checked)} />
      </td>
      <td>
        <span className="id-cell">
          {record.id}
          <IconButton label={t("records.copyId")} icon={RiFileCopyLine} onClick={copy} />
        </span>
      </td>
      {columns.map((field) => (
        <RecordCell key={field.id} field={field} value={record[field.name]} />
      ))}
      {system.map((name) => {
        const value = record[name];
        return (
          <td key={name} title={typeof value === "string" ? value : undefined}>
            {typeof value === "string" ? shortDate(value) : <span className="na">{t("common.na")}</span>}
          </td>
        );
      })}
    </tr>
  );
}

function RecordCell({ field, value }: { field: CollectionField; value: unknown }) {
  const t = useT();
  const view = cellView(field, value);
  switch (view.kind) {
    case "empty":
      return <td><span className="na">{t("common.na")}</span></td>;
    case "number":
      return <td className="num">{view.text}</td>;
    case "bool":
      return <td className={view.value ? undefined : "cell-false"}>{String(view.value)}</td>;
    case "date":
      return <td title={view.title}>{view.text}</td>;
    case "json":
      return <td className="cell-json" title={view.text}>{view.text}</td>;
    case "chips":
      return (
        <td>
          {view.items.map((item, index) => (
            <span key={index} className={view.mono ? "chip mono" : "chip"} title={item}>{item}</span>
          ))}
          {view.more > 0 && <span className="na">+{view.more}</span>}
        </td>
      );
    case "file":
      return (
        <td>
          <span className="cell-file" title={t("records.filesInPane")}>
            <RiAttachment2 size={14} aria-hidden />
          </span>
        </td>
      );
    case "text":
      return <td title={view.text.length > 40 ? view.text : undefined}>{view.text}</td>;
  }
}
