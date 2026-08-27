import { useEffect, useState } from "react";
import type { CollectionDefinition } from "../api/collections";
import type { RecordData, RecordPage } from "../api/records";
import { useSession } from "../auth/session";

function display(value: RecordData[string]): string {
  if (value === null) return "null";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

export function RecordsPage({ collectionName, navigate }: { collectionName: string; navigate(path: string): void }) {
  const { collections, records } = useSession();
  const [collection, setCollection] = useState<CollectionDefinition | null>(null);
  const [page, setPage] = useState<RecordPage | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([collections.catalog(controller.signal), records.list(collectionName, undefined, controller.signal)])
      .then(([catalog, result]) => {
        const definition = catalog.collections[collectionName];
        if (!definition) { setError(true); return; }
        setCollection(definition);
        setPage(result);
      }).catch(() => { if (!controller.signal.aborted) setError(true); });
    return () => controller.abort();
  }, [collectionName, collections, records]);

  async function next() {
    if (!page?.nextCursor) return;
    try { setPage(await records.list(collectionName, page.nextCursor)); } catch { setError(true); }
  }

  const columns = collection ? ["id", "created_at", "updated_at", ...collection.fields.slice().sort((a, b) => a.ordinal - b.ordinal).map((field) => field.name)] : [];
  return <section>
    <div className="page-heading"><h2>{collectionName} records</h2><button onClick={() => navigate(`/admin/collections/${encodeURIComponent(collectionName)}/records/new`)} type="button">Create record</button></div>
    {error && <p role="alert">Unable to load records.</p>}
    {!page && !error && <p aria-live="polite">Loading records…</p>}
    {page && page.items.length === 0 && <p>No records yet.</p>}
    {page && page.items.length > 0 && <div className="table-scroll"><table><thead><tr>{columns.map((column) => <th key={column}>{column}</th>)}</tr></thead><tbody>
      {page.items.map((record, index) => <tr key={String(record.id ?? index)}>{columns.map((column) => <td key={column}>{column === "id" ? <button className="text-button" onClick={() => navigate(`/admin/collections/${encodeURIComponent(collectionName)}/records/${encodeURIComponent(String(record.id))}`)} type="button">{display(record[column])}</button> : display(record[column])}</td>)}</tr>)}
    </tbody></table></div>}
    {page?.nextCursor && <button onClick={() => void next()} type="button">Next page</button>}
  </section>;
}
