import type { ReactNode } from "react";
import { stringifyJSON } from "../backend/json";
import type { FieldDefinition, RecordData } from "../backend/ports";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { useRecordList } from "../workspace/record-workspace";

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
  const page = list.page;

  return <section>
    <div className="page-heading"><h2>{collectionName} records</h2><AdminLink className="link-button" navigate={navigate} route={{ kind: "record", collection: collectionName }}>Create record</AdminLink></div>
    <form className="filter-form" onSubmit={(event) => { event.preventDefault(); list.applyFilter(); }}>
      <label htmlFor="record-filter">Filter</label>
      <input id="record-filter" onChange={(event) => list.setFilterText(event.target.value)} placeholder={`e.g. title = 'value' && rank >= 2`} value={list.filterText} />
      <button type="submit">Apply filter</button>
      {list.filtered && <button onClick={list.clear} type="button">Clear</button>}
    </form>
    {list.filterInvalid && <p role="alert">The filter or sort expression is invalid.</p>}
    {list.failed && <p role="alert">Unable to load records.</p>}
    {!page && !list.failed && !list.filterInvalid && <p aria-live="polite">Loading records…</p>}
    {page && page.items.length === 0 && <p>No records yet.</p>}
    {page && page.items.length > 0 && <div className="table-scroll"><table><thead><tr>{list.columns.map((column) => <th aria-sort={list.sortDirection(column)} key={column}>{list.sortable(column) ? <button className="text-button" onClick={() => list.toggleSort(column)} type="button">{column}{list.sortDirection(column) === "ascending" ? " ↑" : list.sortDirection(column) === "descending" ? " ↓" : ""}</button> : column}</th>)}</tr></thead><tbody>
      {page.items.map((record, index) => <tr key={String(record.id ?? index)}>{list.columns.map((column) => <td key={column}>
        {column === "id"
          ? <AdminLink className="text-button" navigate={navigate} route={{ kind: "record", collection: collectionName, id: String(record.id) }}>{display(record[column])}</AdminLink>
          : displayCell(record[column], list.fieldFor(column))}
      </td>)}</tr>)}
    </tbody></table></div>}
    {list.hasPrevious && <button onClick={list.previousPage} type="button">Previous page</button>}
    {page?.nextCursor && <button onClick={list.nextPage} type="button">Next page</button>}
  </section>;
}
