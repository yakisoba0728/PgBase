import { useEffect, useState } from "react";
import type { CollectionCatalog } from "../api/collections";
import { useSession } from "../auth/session";

interface CollectionsPageProps { navigate(path: string): void }

export function CollectionsPage({ navigate }: CollectionsPageProps) {
  const { collections } = useSession();
  const [catalog, setCatalog] = useState<CollectionCatalog | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setCatalog(null);
    setFailed(false);
    void collections.catalog(controller.signal).then((result) => {
      setCatalog(result);
      setFailed(false);
    }).catch(() => {
      if (!controller.signal.aborted) setFailed(true);
    });
    return () => controller.abort();
  }, [collections]);

  return <section>
    <div className="page-heading">
      <div><h2>Collections</h2>{catalog && <p>Catalog version {catalog.version}</p>}</div>
      <button className="link-button" onClick={() => navigate("/admin/collections/new")} type="button">Create collection</button>
    </div>
    {failed && <p role="alert">Unable to load collections.</p>}
    {!catalog && !failed && <p aria-live="polite">Loading collections…</p>}
    {catalog && Object.values(catalog.collections).length === 0 && <p>No collections yet.</p>}
    {catalog && <ul className="collection-list">
      {Object.values(catalog.collections).sort((a, b) => a.name.localeCompare(b.name)).map((collection) => {
        const relationCount = collection.fields.filter((field) => field.type === "relation").length;
        return <li key={collection.name}>
          <button className="text-button" onClick={() => navigate(`/admin/collections/${encodeURIComponent(collection.name)}/records`)} type="button">{collection.name}</button>
          <span>{collection.fields.length} fields{relationCount > 0 ? ` · ${relationCount} ${relationCount === 1 ? "relation" : "relations"}` : ""}</span>
          <button className="text-button" onClick={() => navigate(`/admin/collections/${encodeURIComponent(collection.name)}/edit`)} type="button">Edit</button>
        </li>;
      })}
    </ul>}
  </section>;
}
