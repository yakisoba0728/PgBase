import { useEffect, useState } from "react";
import { APIClientError } from "../api/client";
import type { CollectionDefinition } from "../api/collections";
import type { RecordData, RecordPage } from "../api/records";
import { stringifyJSON } from "../api/json";
import { useSession } from "../auth/session";

function display(value: RecordData[string]): string {
  if (value === null) return "null";
  if (typeof value === "object") return stringifyJSON(value);
  return String(value);
}

const sortableColumns = new Set(["id", "created_at", "updated_at"]);

export function RecordsPage({ collectionName, navigate }: { collectionName: string; navigate(path: string): void }) {
  const { collections, records } = useSession();
  const [collection, setCollection] = useState<CollectionDefinition | null>(null);
  const [page, setPage] = useState<RecordPage | null>(null);
  const [error, setError] = useState(false);
  const [filterText, setFilterText] = useState("");
  const [appliedFilter, setAppliedFilter] = useState("");
  const [sortText, setSortText] = useState("");
  const [filterError, setFilterError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void collections.catalog(controller.signal).then((catalog) => {
      const definition = catalog.collections[collectionName];
      if (!definition) { setError(true); return; }
      setCollection(definition);
    }).catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [collectionName, collections]);

  useEffect(() => {
    const controller = new AbortController();
    setPage(null);
    void records.list(collectionName, { filter: appliedFilter, sort: sortText }, controller.signal)
      .then((result) => {
        setPage(result);
        setFilterError(false);
        setError(false);
      }).catch((caught: unknown) => {
        if (controller.signal.aborted) return;
        if (caught instanceof APIClientError && caught.code === "validation_failed") {
          setFilterError(true);
        } else setError(true);
      });
    return () => controller.abort();
  }, [appliedFilter, collectionName, records, sortText]);

  async function next() {
    if (!page?.nextCursor) return;
    try { setPage(await records.list(collectionName, { cursor: page.nextCursor, filter: appliedFilter, sort: sortText })); } catch { setError(true); }
  }

  function applyFilter(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setAppliedFilter(filterText.trim());
  }

  function toggleSort(column: string) {
    const [currentField, currentDirection] = sortText.startsWith("-") ? [sortText.slice(1), "desc"] : [sortText, "asc"];
    if (currentField !== column) {
      setSortText(column);
    } else if (currentDirection === "asc" && !sortText.startsWith("-")) {
      setSortText(`-${column}`);
    } else {
      setSortText("");
    }
  }

  const columns = collection ? ["id", "created_at", "updated_at", ...collection.fields.slice().sort((a, b) => a.ordinal - b.ordinal).map((field) => field.name)] : [];
  const sortDirection = (column: string): "ascending" | "descending" | undefined => {
    if (sortText === column) return "ascending";
    if (sortText === `-${column}`) return "descending";
    return undefined;
  };
  const sortable = (column: string) => sortableColumns.has(column) || (collection?.fields.some((field) => field.name === column && field.type !== "jsonb") ?? false);

  return <section>
    <div className="page-heading"><h2>{collectionName} records</h2><button onClick={() => navigate(`/admin/collections/${encodeURIComponent(collectionName)}/records/new`)} type="button">Create record</button></div>
    <form className="filter-form" onSubmit={applyFilter}>
      <label htmlFor="record-filter">Filter</label>
      <input id="record-filter" onChange={(event) => setFilterText(event.target.value)} placeholder={`e.g. title = 'value' && rank >= 2`} value={filterText} />
      <button type="submit">Apply filter</button>
      {(appliedFilter !== "" || sortText !== "") && <button onClick={() => { setFilterText(""); setAppliedFilter(""); setSortText(""); }} type="button">Clear</button>}
    </form>
    {filterError && <p role="alert">The filter or sort expression is invalid.</p>}
    {error && <p role="alert">Unable to load records.</p>}
    {!page && !error && !filterError && <p aria-live="polite">Loading records…</p>}
    {page && page.items.length === 0 && <p>No records yet.</p>}
    {page && page.items.length > 0 && <div className="table-scroll"><table><thead><tr>{columns.map((column) => <th aria-sort={sortDirection(column)} key={column}>{sortable(column) ? <button className="text-button" onClick={() => toggleSort(column)} type="button">{column}{sortDirection(column) === "ascending" ? " ↑" : sortDirection(column) === "descending" ? " ↓" : ""}</button> : column}</th>)}</tr></thead><tbody>
      {page.items.map((record, index) => <tr key={String(record.id ?? index)}>{columns.map((column) => <td key={column}>{column === "id" ? <button className="text-button" onClick={() => navigate(`/admin/collections/${encodeURIComponent(collectionName)}/records/${encodeURIComponent(String(record.id))}`)} type="button">{display(record[column])}</button> : display(record[column])}</td>)}</tr>)}
    </tbody></table></div>}
    {page?.nextCursor && <button onClick={() => void next()} type="button">Next page</button>}
  </section>;
}
