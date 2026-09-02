import { useEffect, useState } from "react";
import type { CollectionCatalog } from "../backend/ports";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { useBackend } from "../auth/session";

interface CollectionsPageProps { navigate(route: AdminRoute): void }

export function CollectionsPage({ navigate }: CollectionsPageProps) {
  const { schema } = useBackend();
  const [catalog, setCatalog] = useState<CollectionCatalog | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setCatalog(null);
    setFailed(false);
    void schema.catalog(controller.signal).then((result) => {
      setCatalog(result);
      setFailed(false);
    }).catch(() => {
      if (!controller.signal.aborted) setFailed(true);
    });
    return () => controller.abort();
  }, [schema]);

  return <section>
    <div className="page-heading">
      <div><h2>Collections</h2>{catalog && <p>Catalog version {catalog.version}</p>}</div>
      <AdminLink className="link-button" navigate={navigate} route={{ kind: "collection-create" }}>Create collection</AdminLink>
    </div>
    {failed && <p role="alert">Unable to load collections.</p>}
    {!catalog && !failed && <p aria-live="polite">Loading collections…</p>}
    {catalog && catalog.all().length === 0 && <p>No collections yet.</p>}
    {catalog && <ul className="collection-list">
      {catalog.all().map((collection) => {
        const relationCount = collection.fields.filter((field) => field.type === "relation").length;
        return <li key={collection.name}>
          <AdminLink className="text-button" navigate={navigate} route={{ kind: "records", collection: collection.name }}>{collection.name}</AdminLink>
          <span>{collection.fields.length} fields{relationCount > 0 ? ` · ${relationCount} ${relationCount === 1 ? "relation" : "relations"}` : ""}</span>
          <AdminLink className="text-button" navigate={navigate} route={{ kind: "collection-edit", collection: collection.name }}>Edit</AdminLink>
        </li>;
      })}
    </ul>}
  </section>;
}
