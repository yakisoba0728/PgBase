import { memo, type MouseEvent } from "react";
import { useQuery } from "@tanstack/react-query";
import { RiExpandDiagonal2Line } from "@remixicon/react";
import { stringify } from "lossless-json";
import type { Collection, CollectionField } from "../api/collections";
import type { RecordData } from "../api/records";
import { useT } from "../i18n";
import { FileThumb } from "./FileThumb";
import { cellView, fieldKind, shortDate } from "./fields";
import { recordFilesQuery } from "./queries";
import { labelField, recordLabel } from "./relation";

/** Fixed column widths: the grid uses table-layout: fixed so rows can be windowed without reflow. */
export function columnWidth(field: CollectionField | undefined): number {
  if (!field) return 176;
  switch (fieldKind(field)) {
    case "bool":
      return 96;
    case "number":
      return field.type === "integer" ? 112 : 150;
    case "date":
    case "autodate":
      return 176;
    case "geo":
    case "select":
    case "file":
      return 150;
    case "editor":
    case "json":
      return 260;
    case "relations":
    case "multiselect":
      return 240;
    default:
      return 220;
  }
}

// Past this many characters a cell offers the full value in a panel.
const longText = 36;

export type OnPeek = (label: string, text: string, anchor: HTMLElement) => void;

interface CellProps {
  field: CollectionField | undefined;
  /** Name of a system column when `field` is absent. */
  name: string;
  record: RecordData;
  collection: string;
  catalog: Collection[];
  /** False while scrolling: per-row requests (file lists) wait until the grid settles. */
  settled: boolean;
  onPeek: OnPeek;
}

/** One grid cell; memoised because a scroll re-renders every visible row. */
export const RecordCell = memo(function RecordCell({ field, name, record, collection, catalog, settled, onPeek }: CellProps) {
  const t = useT();
  const value = record[name];
  if (!field) {
    return <td title={typeof value === "string" ? value : undefined}>{typeof value === "string" ? shortDate(value) : <span className="na">{t("common.na")}</span>}</td>;
  }
  const view = cellView(field, value);
  const peek = (full: () => string) => (event: MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    onPeek(name, full(), event.currentTarget);
  };
  const peekButton = (full: () => string) => (
    <button type="button" className="peek-btn" tabIndex={-1} aria-label={t("peek.show", { name })} title={t("peek.show", { name })} onClick={peek(full)}>
      <RiExpandDiagonal2Line size={14} aria-hidden />
    </button>
  );
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
      return (
        <td className="cell-json cell-long">
          <span className="cell-text">{view.text}</span>
          {peekButton(() => stringify(value, null, 2) ?? "")}
        </td>
      );
    case "chips":
      return (
        <td>
          {view.items.map((item, index) => (
            <span key={index} className={view.mono ? "chip mono" : "chip"} title={item}>{item}</span>
          ))}
          {view.more > 0 && <span className="na">+{view.more}</span>}
        </td>
      );
    case "relation": {
      const target = catalog.find((candidate) => candidate.id === field.relation?.targetCollectionId);
      const expanded = record.expand?.[name];
      const related = Array.isArray(expanded) ? expanded : expanded ? [expanded] : [];
      const label = target ? labelField(target) : undefined;
      return (
        <td>
          {view.ids.slice(0, 3).map((id) => {
            const text = recordLabel(related.find((item) => item.id === id), label, id);
            return <span key={id} className={text === id ? "chip mono" : "chip"} title={id}>{text}</span>;
          })}
          {view.ids.length > 3 && <span className="na">+{view.ids.length - 3}</span>}
        </td>
      );
    }
    case "file":
      return <FileCell collection={collection} record={record.id} field={field.name} settled={settled} />;
    case "text":
      return (
        <td className={view.text.length > longText ? "cell-long" : undefined}>
          <span className="cell-text">{view.text}</span>
          {view.text.length > longText && peekButton(() => (typeof value === "string" ? value : String(stringify(value))))}
        </td>
      );
  }
});

function FileCell({ collection, record, field, settled }: { collection: string; record: string; field: string; settled: boolean }) {
  const files = useQuery({ ...recordFilesQuery(collection, record, field), enabled: settled });
  const items = files.data ?? [];
  return (
    <td>
      <span className="cell-files">
        {items.slice(0, 3).map((file) => (
          <FileThumb key={file.id} file={file} />
        ))}
        {items.length > 3 && <span className="na">+{items.length - 3}</span>}
        {files.data && items.length === 0 && <span className="na">–</span>}
      </span>
    </td>
  );
}
