import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode, type RefObject } from "react";
import { RiArrowDownLine, RiArrowUpLine } from "@remixicon/react";
import type { Collection, CollectionField } from "../api/collections";
import type { RecordData } from "../api/records";
import { CopyButton } from "../components/CopyButton";
import { Checkbox, GroupCheckbox } from "../components/Field";
import { enterRows, opensRow, Table, useArrivals } from "../components/Table";
import { tokenPx, useVirtualRows, visibleRange } from "../components/virtualRows";
import { useT } from "../i18n";
import { fieldKind } from "./fields";
import { columnWidth, RecordCell, type OnPeek } from "./RecordCell";

interface RecordsTableProps {
  collection: Collection;
  catalog: Collection[];
  /** Visible column names in display order (id is always first and not listed). */
  columns: string[];
  records: RecordData[];
  sort: string;
  onSort: (field: string) => void;
  openId: string | null;
  onOpen: (id: string) => void;
  selected: Set<string>;
  /** `range` selects every row between the last clicked one and this one (shift-click). */
  onSelect: (ids: string[], checked: boolean) => void;
  scroller: RefObject<HTMLDivElement | null>;
  onPeek: OnPeek;
}

// The server sorts only on indexed or unique columns (else 422 sort: unindexed_field), except
// in SQL views, and never on json, file or password fields; of the system columns, id and created_at.
const sortableType = (field: CollectionField) => !["file", "relations", "json", "geo", "multiselect", "password"].includes(fieldKind(field));
const sortableSystem = ["id", "created_at"];
// Read once per chunk load, after the stylesheets: --row is each row's height, the head adds the 4px gap above the first row.
const rowHeight = tokenPx("--row", 48);
const headHeight = tokenPx("--row-head", 40) + tokenPx("--sp-4", 4);
const checkWidth = tokenPx("--grid-check", 44);
const idWidth = 316;

/** The records grid: fixed layout, sticky header and id column, only the visible rows in the DOM. */
export function RecordsTable({ collection, catalog, columns, records, sort, onSort, openId, onOpen, selected, onSelect, scroller, onPeek }: RecordsTableProps) {
  const t = useT();
  const sqlView = Boolean(collection.view?.sql);
  const byName = useMemo(() => new Map(collection.fields.map((field) => [field.name, field])), [collection.fields]);
  const rows = useVirtualRows(scroller, records.length, rowHeight);
  const checkedCount = records.filter((record) => selected.has(record.id)).length;
  const [focus, setFocus] = useState(0);
  // The record last checked, by id: a sort, filter or prepend moves rows, so an index would anchor elsewhere.
  const anchor = useRef<string | null>(null);
  const focusPending = useRef(false);
  const tbody = useRef<HTMLTableSectionElement>(null);
  const ids = useMemo(() => records.map((record) => record.id), [records]);
  const arrived = useArrivals(ids);
  // A new list (the first page, a filter or a sort) enters staggered; a prepended record or a further page does not,
  // and nothing replays while scrolling, since that changes which rows are rendered, not the list.
  const firstIds = useRef<string[] | null>(null);
  useLayoutEffect(() => {
    const previous = firstIds.current;
    firstIds.current = ids.slice(0, 3);
    if (!ids.length || previous?.some((id) => firstIds.current?.includes(id))) return;
    enterRows([...(tbody.current?.querySelectorAll<HTMLElement>("tr[data-index]") ?? [])]);
  }, [ids]);
  const width = checkWidth + idWidth + columns.reduce((sum, name) => sum + columnWidth(byName.get(name)), 0);

  // Moves keyboard focus to row `index`, scrolling it into the rendered window first.
  const moveTo = (index: number) => {
    const next = Math.max(0, Math.min(records.length - 1, index));
    const element = scroller.current;
    if (element) {
      const top = next * rowHeight;
      if (top < element.scrollTop) element.scrollTop = top;
      else if (top + rowHeight + headHeight > element.scrollTop + element.clientHeight) element.scrollTop = top + rowHeight + headHeight - element.clientHeight;
    }
    focusPending.current = true;
    setFocus(next);
  };
  useEffect(() => {
    if (!focusPending.current) return;
    const row = tbody.current?.querySelector<HTMLElement>(`tr[data-index="${focus}"]`);
    if (row) {
      row.focus({ preventScroll: true });
      focusPending.current = false;
    }
  });

  const onRowKey = (event: KeyboardEvent<HTMLTableRowElement>, index: number, record: RecordData) => {
    if (event.target !== event.currentTarget) return;
    const page = Math.max(1, Math.floor((scroller.current?.clientHeight ?? 400) / rowHeight) - 1);
    const moves: Record<string, number> = { ArrowDown: 1, ArrowUp: -1, PageDown: page, PageUp: -page, Home: -Infinity, End: Infinity };
    if (event.key in moves) {
      event.preventDefault();
      moveTo(Number.isFinite(moves[event.key]) ? index + moves[event.key] : moves[event.key] < 0 ? 0 : records.length - 1);
    } else if (event.key === "Enter") {
      event.preventDefault();
      onOpen(record.id);
    } else if (event.key === " ") {
      event.preventDefault();
      onSelect([record.id], !selected.has(record.id));
    }
  };

  const onCheck = (index: number, checked: boolean, shift: boolean) => {
    const from = shift && anchor.current !== null ? records.findIndex((record) => record.id === anchor.current) : -1;
    if (from >= 0) onSelect(records.slice(Math.min(from, index), Math.max(from, index) + 1).map((record) => record.id), checked);
    else onSelect([records[index].id], checked);
    anchor.current = records[index].id;
  };

  // Rows are memoised; they get stable handlers that always call the latest closures.
  const latest = useRef({ onRowKey, onCheck });
  latest.current = { onRowKey, onCheck };
  const stableKey = useCallback((event: KeyboardEvent<HTMLTableRowElement>, index: number, record: RecordData) => latest.current.onRowKey(event, index, record), []);
  const stableCheck = useCallback((index: number, checked: boolean, shift: boolean) => latest.current.onCheck(index, checked, shift), []);

  const header = (name: string, children: ReactNode, canSort: boolean) => {
    const direction = sort === name ? "asc" : sort === `-${name}` ? "desc" : undefined;
    if (!canSort) return <span className="th-label">{children}</span>;
    return (
      <button type="button" className="sort-btn" data-sorted={direction ? "" : undefined} onClick={() => onSort(name)}>
        {children}
        {direction === "asc" && <RiArrowUpLine size={14} aria-label={t("records.sortedAsc")} />}
        {direction === "desc" && <RiArrowDownLine size={14} aria-label={t("records.sortedDesc")} />}
      </button>
    );
  };
  const ariaSort = (name: string) => (sort === name ? "ascending" : sort === `-${name}` ? "descending" : undefined);
  const visible = records.slice(rows.start, rows.end);
  // Per-row requests go to the rows on screen once scrolling stops, not to the overscan.
  const screen = !rows.scrolling && scroller.current ? visibleRange(scroller.current.scrollTop, scroller.current.clientHeight, rowHeight, records.length, 0) : { start: 0, end: 0 };
  const span = columns.length + 2;
  // The roving tab stop: the focused row, or the first rendered one once that row has scrolled out.
  const focusRow = Math.min(focus, records.length - 1);
  const tabStop = focusRow >= rows.start && focusRow < rows.end ? focusRow : rows.start;

  return (
    <Table className="records-grid" label={collection.name} aria-rowcount={records.length + 1} style={{ width }}>
      <colgroup>
        <col style={{ width: checkWidth }} />
        <col style={{ width: idWidth }} />
        {columns.map((name) => (
          <col key={name} style={{ width: columnWidth(byName.get(name)) }} />
        ))}
      </colgroup>
      <thead>
        <tr aria-rowindex={1}>
          <th className="col-check sticky-check">
            <GroupCheckbox label={t("records.selectAll")} chosen={checkedCount} total={records.length} onChange={(on) => onSelect(records.map((record) => record.id), on)} />
          </th>
          <th className="sticky-id" aria-sort={ariaSort("id")}>{header("id", "id", true)}</th>
          {columns.map((name) => {
            const field = byName.get(name);
            const canSort = field ? sortableType(field) && (sqlView || field.indexed || field.unique) : sqlView || sortableSystem.includes(name);
            return (
              <th key={name} className={field && fieldKind(field) === "number" ? "num" : undefined} aria-sort={ariaSort(name)} title={field?.help}>
                {header(name, field ? <>{name} <span className="th-type">{field.type}</span></> : name, canSort)}
              </th>
            );
          })}
        </tr>
      </thead>
      <tbody ref={tbody}>
        {rows.before > 0 && (
          <tr className="grid-spacer" aria-hidden>
            <td colSpan={span} style={{ height: rows.before }} />
          </tr>
        )}
        {visible.map((record, offset) => {
          const index = rows.start + offset;
          return (
            <RecordRow
              key={record.id}
              index={index}
              record={record}
              collection={collection}
              catalog={catalog}
              columns={columns}
              byName={byName}
              current={record.id === openId}
              arrived={arrived.has(record.id)}
              checked={selected.has(record.id)}
              focusable={index === tabStop}
              settled={index >= screen.start && index < screen.end}
              onOpen={onOpen}
              onCheck={stableCheck}
              onKey={stableKey}
              onFocusRow={setFocus}
              onPeek={onPeek}
            />
          );
        })}
        {rows.after > 0 && (
          <tr className="grid-spacer" aria-hidden>
            <td colSpan={span} style={{ height: rows.after }} />
          </tr>
        )}
      </tbody>
    </Table>
  );
}

interface RecordRowProps {
  index: number;
  record: RecordData;
  collection: Collection;
  catalog: Collection[];
  columns: string[];
  byName: Map<string, CollectionField>;
  current: boolean;
  /** Just created: the brief arrival wash. */
  arrived: boolean;
  checked: boolean;
  focusable: boolean;
  settled: boolean;
  onOpen: (id: string) => void;
  onCheck: (index: number, checked: boolean, shift: boolean) => void;
  onKey: (event: KeyboardEvent<HTMLTableRowElement>, index: number, record: RecordData) => void;
  onFocusRow: (index: number) => void;
  onPeek: OnPeek;
}

const RecordRow = memo(function RecordRow({ index, record, collection, catalog, columns, byName, current, arrived, checked, focusable, settled, onOpen, onCheck, onKey, onFocusRow, onPeek }: RecordRowProps) {
  const t = useT();
  const stop = (event: MouseEvent) => event.stopPropagation();
  return (
    <tr
      data-index={index}
      aria-rowindex={index + 2}
      tabIndex={focusable ? 0 : -1}
      aria-current={current || undefined}
      data-arrived={arrived ? "" : undefined}
      data-checked={checked ? "" : undefined}
      // Windowed rows keep their own roving focus and keys, so only the click rule of the shared Row applies.
      onClick={(event) => opensRow(event) && onOpen(record.id)}
      onKeyDown={(event) => onKey(event, index, record)}
      onFocus={(event) => event.target === event.currentTarget && onFocusRow(index)}
    >
      <td className="col-check sticky-check" onClick={stop}>
        <Checkbox
          aria-label={t("records.select", { id: record.id })}
          checked={checked}
          onChange={(event) => onCheck(index, event.target.checked, (event.nativeEvent as globalThis.MouseEvent).shiftKey)}
        />
      </td>
      <td className="sticky-id">
        <span className="id-cell">
          <span className="cell-text">{record.id}</span>
          <CopyButton size="xs" className="icon-btn-raised" label={t("common.copyId")} value={record.id} />
        </span>
      </td>
      {columns.map((name) => (
        <RecordCell key={name} name={name} field={byName.get(name)} record={record} collection={collection.name} catalog={catalog} settled={settled} onPeek={onPeek} />
      ))}
    </tr>
  );
});
