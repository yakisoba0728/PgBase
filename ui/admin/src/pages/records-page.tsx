import { Button, EmptyState, PageHeading } from "../components/ui";
import { CollectionAPIExamples } from "./collection-api-examples";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useSession } from "../auth/session";
import { stringifyJSON } from "../backend/json";
import type { FieldDefinition, RecordData } from "../backend/ports";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { useRecordList } from "../workspace/record-workspace";
import { readColumns, writeColumns } from "../workspace/record-productivity";
import { WorkspaceIcon } from "../components/workspace-icon";

function display(value: RecordData[string]): string {
  if (value === null) return "null";
  if (typeof value === "object") return stringifyJSON(value);
  return String(value);
}

function displayCell(value: RecordData[string], field: FieldDefinition | undefined): ReactNode {
  if (field?.type === "password") return <span aria-label="Password is not disclosed">—</span>;
  if (field?.type === "editor" && typeof value === "string") {
    const document = new DOMParser().parseFromString(value.slice(0, 16384), "text/html");
    const text = document.body.textContent ?? "";
    return text.slice(0, 160) + (text.length > 160 || value.length > 16384 ? "…" : "");
  }
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
  const [limit, setLimit] = useState(50);
  const [projection, setProjection] = useState("");
  const [expand, setExpand] = useState("");
  const [queryError, setQueryError] = useState("");
  const pageSelection = useRef<HTMLInputElement>(null);
  function applyQuery() {
    if (!Number.isInteger(limit) || limit < 1 || limit > 200) { setQueryError("Page size must be from 1 through 200."); return; }
    setQueryError("");
    const fields = projection.split(",").map(v => v.trim()).filter(Boolean);
    list.setQueryOptions({ limit, ...(fields.length ? { fields: [...new Set(["id", ...fields])].join(",") } : {}), ...(expand.trim() ? { expand: expand.trim() } : {}) });
  }
  const readOnly = list.collection?.view !== undefined;
  const writable = list.collection !== null && !readOnly;
  const admin = session.kind === "authenticated" ? session.administrator.id : "anonymous";
  const [visible, setVisible] = useState<string[] | null>(null);
  const anchor = useRef<number | null>(null);
  useEffect(() => { if (list.columns.length > 0) setVisible(readColumns({ administrator: admin, collection: collectionName }, list.columns)); }, [admin, collectionName, list.columns.join("\0")]);
  const columns = useMemo(() => {
    const requested = list.queryOptions.fields?.split(",").map(v => v.trim());
    return (visible ?? list.columns).filter(name => !requested || requested.includes("*") || name === "id" || requested.some(field => field.split(":")[0] === name));
  }, [list.columns, visible, list.queryOptions.fields]);
  const ids = page?.items.map((record) => String(record.id)) ?? [];
  const selectedOnPage = ids.filter(id => list.selected.has(id)).length;
  useEffect(() => { if (pageSelection.current) pageSelection.current.indeterminate = selectedOnPage > 0 && selectedOnPage < ids.length; }, [selectedOnPage, ids.length]);
  useEffect(() => { anchor.current = null; }, [page]);
  function showColumn(column: string, show: boolean) {
    const stored = visible ?? list.columns;
    const next = show ? list.columns.filter((name) => name === column || stored.includes(name)) : stored.filter((name) => name !== column);
    setVisible(next); writeColumns({ administrator: admin, collection: collectionName }, next);
  }
  function selectRow(index: number, checked: boolean, shift: boolean) {
    if (shift && anchor.current !== null) list.selectMany(ids.slice(Math.min(anchor.current, index), Math.max(anchor.current, index) + 1), checked);
    else list.select(ids[index], checked);
    anchor.current = index;
  }

  return <section className="records-page">
    <PageHeading title={`${collectionName} records`} description={readOnly ? "View collection" : "Collection records"} actions={<div className="pb-records-header-actions">
      <a className="text-button" href="#record-api-preview"><WorkspaceIcon name="terminal" />API preview</a>
      <AdminLink className="link-button secondary" navigate={navigate} route={{ kind: "collection-edit", collection: collectionName }}><WorkspaceIcon name="settings" />Collection settings</AdminLink>
      {writable && <AdminLink className="link-button" navigate={navigate} route={{ kind: "record", collection: collectionName }}><WorkspaceIcon name="plus" />Create record</AdminLink>}
    </div>} />
    {readOnly && <p>This view is read-only.</p>}
    <div className="pb-records-toolbar">
    <form className="filter-form" onSubmit={(event) => { event.preventDefault(); list.applyFilter(); }}>
      <label className="sr-only" htmlFor="record-filter">Filter</label>
      <div className="record-filter-input">
      <input id="record-filter" onChange={(event) => list.setFilterText(event.target.value)} placeholder={`e.g. title = 'value' && rank >= 2`} value={list.filterText} />
      <Button variant="secondary" type="submit" disabled={list.loading}>Apply filter</Button>
      {list.filtered && <Button variant="secondary" onClick={list.clear}>Clear</Button>}
      </div>
    </form>
    <div className="pb-records-tools"><Button variant="secondary" disabled={list.loading} onClick={list.refresh}><WorkspaceIcon name="activity" />Refresh records</Button>
    <details className="advanced-options"><summary>Query options</summary><form onSubmit={e => { e.preventDefault(); applyQuery(); }}><div className="settings-field-grid">
      <label>Records per page<input type="number" min={1} max={200} step={1} value={limit} onChange={e => setLimit(Number(e.target.value))} /></label>
      <label>Server fields<input aria-label="Server fields" value={projection} onChange={e => setProjection(e.target.value)} placeholder="title, created_at" /><span className="settings-help">Comma-separated field projection. ID is always included for row actions.</span></label>
      <label>Expand relations<input aria-label="Expand relations" value={expand} onChange={e => setExpand(e.target.value)} placeholder="author, tags" /><span className="settings-help">Comma-separated relation names. Cursor pagination keeps the selected query.</span></label>
      </div>{queryError && <p role="alert">{queryError}</p>}<Button variant="secondary" type="submit">Apply query options</Button></form></details>
    {page && <div className="record-actions">
      <details><summary>Columns</summary>{list.columns.map((column) => <label key={column}><input checked={(visible ?? list.columns).includes(column)} disabled={column === "id"} onChange={(event) => showColumn(column, event.target.checked)} type="checkbox" />{column}</label>)}</details>
      <button disabled={list.actionPending} onClick={list.exportJSON} type="button">{list.selected.size > 0 ? `Export ${list.selected.size} selected as JSON` : "Export filtered records as JSON"}</button>
      {writable && list.selected.size > 0 && <button className="danger" disabled={list.actionPending} onClick={list.deleteSelected} type="button">Delete {list.selected.size} selected</button>}
    </div>}
    </div></div>
    {expand.trim() && list.page?.items.some(record => record.expand !== undefined) && <details><summary>Expanded relation data</summary><pre>{stringifyJSON(list.page.items.map(record => ({ id: record.id, expand: record.expand })), 2)}</pre></details>}
    {list.actionMessage && <p aria-live="polite">{list.actionMessage}</p>}
    {list.filterInvalid && <p role="alert">The filter or sort expression is invalid.</p>}
    {list.failed && <p role="alert">Unable to load records. <Button variant="secondary" onClick={list.refresh}>Retry loading records</Button></p>}
    {!page && !list.failed && !list.filterInvalid && <p aria-live="polite">Loading records…</p>}
    {list.loading && page && <p role="status">Refreshing records…</p>}
    {page && <div className="pb-records-resultbar"><p className="record-page-meta" role="status">{page.items.length} {page.items.length === 1 ? "record" : "records"} on this page{list.selected.size > 0 ? ` · ${list.selected.size} selected` : ""}</p>{list.selected.size > 0 && <Button variant="ghost" disabled={list.actionPending} onClick={() => list.selectMany([...list.selected], false)}>Clear selection</Button>}</div>}
    {page && page.items.length === 0 && <EmptyState title={list.filterApplied ? "No records match this filter." : "No records yet."} description={list.filterApplied ? "Try a different filter, or clear it to see all records." : readOnly ? "This view has no records to display." : "Create a record to start adding data to this collection."} action={list.filterApplied ? <Button variant="secondary" onClick={list.clear}>Clear filter</Button> : writable && <AdminLink className="link-button" navigate={navigate} route={{ kind: "record", collection: collectionName }}>Create your first record</AdminLink>} />}
    {page && page.items.length > 0 && <div className="table-scroll pb-records-table" aria-busy={list.loading} role="region" aria-label={`${collectionName} records`} tabIndex={0}><table><thead><tr>{writable && <th><input ref={pageSelection} aria-label="Select page" checked={ids.length > 0 && selectedOnPage === ids.length} onChange={(event) => list.selectMany(ids, event.target.checked)} type="checkbox" /></th>}{columns.map((column) => <th aria-sort={list.sortDirection(column)} key={column}>{list.fieldFor(column)?.type === "geoPoint" ? <>{column}{(["lon", "lat"] as const).map(coordinate => list.sortable(`${column}.${coordinate}`) && <button key={coordinate} type="button" className="text-button" aria-label={`Sort ${column} ${coordinate}`} onClick={() => list.toggleSort(`${column}.${coordinate}`)}>{coordinate}{list.sortDirection(`${column}.${coordinate}`) === "ascending" ? " ↑" : list.sortDirection(`${column}.${coordinate}`) === "descending" ? " ↓" : ""}</button>)}</> : list.sortable(column) ? <button className="text-button" onClick={() => list.toggleSort(column)} type="button">{column}{list.sortDirection(column) === "ascending" ? " ↑" : list.sortDirection(column) === "descending" ? " ↓" : ""}</button> : column}</th>)}</tr></thead><tbody>
      {page.items.map((record, index) => <tr key={String(record.id ?? index)}>{writable && <td><input aria-label={`Select record ${String(record.id)}`} checked={list.selected.has(String(record.id))} onChange={(event) => selectRow(index, event.target.checked, (event.nativeEvent as MouseEvent).shiftKey)} type="checkbox" /></td>}{columns.map((column) => <td key={column}>
        {column === "id"
          ? !writable ? display(record[column]) : <AdminLink className="text-button" navigate={navigate} route={{ kind: "record", collection: collectionName, id: String(record.id) }}>{display(record[column])}</AdminLink>
          : displayCell(record[column], list.fieldFor(column))}
      </td>)}</tr>)}
    </tbody></table></div>}
    {(list.hasPrevious || page?.nextCursor) && <nav className="page-pagination pb-records-footer" aria-label="Record pagination">{list.hasPrevious && <Button variant="secondary" disabled={list.loading} onClick={list.previousPage}>Previous page</Button>}{page?.nextCursor && <Button variant="secondary" disabled={list.loading} onClick={list.nextPage}>Next page</Button>}</nav>}
    {list.collection && <div className="record-reference" id="record-api-preview"><CollectionAPIExamples collection={list.collection} /></div>}
  </section>;
}
