import { CollectionAPIExamples } from "./collection-api-examples";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useSession } from "../auth/session";
import { stringifyJSON } from "../backend/json";
import type { FieldDefinition, RecordData } from "../backend/ports";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { useRecordList } from "../workspace/record-workspace";
import { readColumns, writeColumns } from "../workspace/record-productivity";

function display(value: RecordData[string]): string {
  if (value === null) return "null";
  if (typeof value === "object") return stringifyJSON(value);
  return String(value);
}

function displayCell(value: RecordData[string], field: FieldDefinition | undefined): ReactNode {
  if (field?.type !== "relation") return display(value);
  if (value === null) return "null";
  if (field.relation?.cardinality === "multiple") {
    if (!Array.isArray(value)) return "Invalid relation value";
    if (value.length === 0) return "[]";
    return <ol aria-label={`${field.name} related record IDs`} className="relation-values">
      {value.map((member, index) => <li key={index}><code>{typeof member === "string" ? member : "Invalid relation value"}</code></li>)}
    </ol>;
  }
  return typeof value === "string" ? <code>{value}</code> : "Invalid relation value";
}

export function RecordsPage({ collectionName, navigate }: { collectionName: string; navigate(route: AdminRoute): void }) {
  const list = useRecordList(collectionName);
  const { state: session } = useSession();
  const page = list.page;
  const readOnly = list.collection?.view !== undefined;
  const admin = session.kind === "authenticated" ? session.administrator.id : "anonymous";
  const [visible, setVisible] = useState<string[] | null>(null);
  const anchor = useRef<number | null>(null);
  useEffect(() => { if (list.columns.length > 0) setVisible(readColumns({ administrator: admin, collection: collectionName }, list.columns)); }, [admin, collectionName, list.columns.join("\0")]);
  const columns = useMemo(() => visible ?? list.columns, [list.columns, visible]);
  const ids = page?.items.map((record) => String(record.id)) ?? [];
  function showColumn(column: string, show: boolean) {
    const next = show ? list.columns.filter((name) => name === column || columns.includes(name)) : columns.filter((name) => name !== column);
    setVisible(next); writeColumns({ administrator: admin, collection: collectionName }, next);
  }
  function selectRow(index: number, checked: boolean, shift: boolean) {
    if (shift && anchor.current !== null) list.selectMany(ids.slice(Math.min(anchor.current, index), Math.max(anchor.current, index) + 1), checked);
    else list.select(ids[index], checked);
    anchor.current = index;
  }

  return <section>
    <div className="page-heading"><h2>{collectionName} records</h2>{!readOnly && <AdminLink className="link-button" navigate={navigate} route={{ kind: "record", collection: collectionName }}>Create record</AdminLink>}</div>
    {readOnly && <p>This view is read-only.</p>}
    {list.collection && <CollectionAPIExamples collection={list.collection} />}
    <form className="filter-form" onSubmit={(event) => { event.preventDefault(); list.applyFilter(); }}>
      <label htmlFor="record-filter">Filter</label>
      <input id="record-filter" onChange={(event) => list.setFilterText(event.target.value)} placeholder={`e.g. title = 'value' && rank >= 2`} value={list.filterText} />
      <button type="submit">Apply filter</button>
      {list.filtered && <button onClick={list.clear} type="button">Clear</button>}
    </form>
    {page && <div className="record-actions">
      <details><summary>Columns</summary>{list.columns.map((column) => <label key={column}><input checked={columns.includes(column)} disabled={column === "id"} onChange={(event) => showColumn(column, event.target.checked)} type="checkbox" />{column}</label>)}</details>
      <button disabled={list.actionPending} onClick={list.exportJSON} type="button">{list.selected.size > 0 ? `Export ${list.selected.size} selected as JSON` : "Export filtered records as JSON"}</button>
      {!readOnly && list.selected.size > 0 && <button className="danger" disabled={list.actionPending} onClick={list.deleteSelected} type="button">Delete {list.selected.size} selected</button>}
    </div>}
    {list.actionMessage && <p aria-live="polite">{list.actionMessage}</p>}
    {list.filterInvalid && <p role="alert">The filter or sort expression is invalid.</p>}
    {list.failed && <p role="alert">Unable to load records.</p>}
    {!page && !list.failed && !list.filterInvalid && <p aria-live="polite">Loading records…</p>}
    {page && page.items.length === 0 && <p>No records yet.</p>}
    {page && page.items.length > 0 && <div className="table-scroll"><table><thead><tr>{!readOnly && <th><input aria-label="Select page" checked={ids.length > 0 && ids.every((id) => list.selected.has(id))} onChange={(event) => list.selectMany(ids, event.target.checked)} type="checkbox" /></th>}{columns.map((column) => <th aria-sort={list.sortDirection(column)} key={column}>{list.sortable(column) ? <button className="text-button" onClick={() => list.toggleSort(column)} type="button">{column}{list.sortDirection(column) === "ascending" ? " ↑" : list.sortDirection(column) === "descending" ? " ↓" : ""}</button> : column}</th>)}</tr></thead><tbody>
      {page.items.map((record, index) => <tr key={String(record.id ?? index)}>{!readOnly && <td><input aria-label={`Select record ${String(record.id)}`} checked={list.selected.has(String(record.id))} onChange={(event) => selectRow(index, event.target.checked, (event.nativeEvent as MouseEvent).shiftKey)} type="checkbox" /></td>}{columns.map((column) => <td key={column}>
        {column === "id"
          ? readOnly ? display(record[column]) : <AdminLink className="text-button" navigate={navigate} route={{ kind: "record", collection: collectionName, id: String(record.id) }}>{display(record[column])}</AdminLink>
          : displayCell(record[column], list.fieldFor(column))}
      </td>)}</tr>)}
    </tbody></table></div>}
    {list.hasPrevious && <button onClick={list.previousPage} type="button">Previous page</button>}
    {page?.nextCursor && <button onClick={list.nextPage} type="button">Next page</button>}
  </section>;
}
