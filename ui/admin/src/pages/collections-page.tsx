import { useCallback, useEffect, useRef, useState } from "react";
import type { CollectionCatalog, CollectionDefinition } from "../backend/ports";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { useBackend } from "../auth/session";

interface CollectionsPageProps { navigate(route: AdminRoute): void }

function TruncateCollection({ collection }: { collection: CollectionDefinition }) {
  const { schema } = useBackend();
  const [state, setState] = useState({ confirming: false, typed: "", pending: false, failed: false, result: null as { recordsDeleted: number; filesDeleted: number; relationsDeleted: number } | null });
  if (collection.view !== undefined) return <span>Views cannot be truncated.</span>;
  if (!state.confirming) return <span>{state.result !== null && <span role="status">Removed {state.result.recordsDeleted} records, {state.result.filesDeleted} files, and {state.result.relationsDeleted} relations. </span>}<button className="danger" disabled={schema.truncate === undefined} type="button" onClick={() => setState({ ...state, confirming: true, failed: false, result: null })}>Truncate records</button></span>;
  async function truncate() {
    if (state.typed !== collection.name || state.pending || schema.truncate === undefined) return;
    setState({ ...state, pending: true, failed: false });
    try {
      const result = await schema.truncate(collection.name, state.typed); setState({ confirming: false, typed: "", pending: false, failed: false, result });
    } catch { setState({ ...state, pending: false, failed: true }); }
  }
  return <span className="truncate-control"><label>Type {collection.name} to permanently remove every record <input aria-label={`Confirm truncate ${collection.name}`} autoComplete="off" disabled={state.pending} value={state.typed} onChange={(event) => setState({ ...state, typed: event.target.value })} /></label><button className="danger" disabled={state.pending || state.typed !== collection.name || schema.truncate === undefined} type="button" onClick={() => void truncate()}>{state.pending ? "Truncating…" : "Confirm truncate"}</button><button className="secondary" disabled={state.pending} type="button" onClick={() => setState({ ...state, confirming: false, typed: "" })}>Cancel</button>{state.failed && <span role="alert"> Unable to truncate this collection.</span>}</span>;
}

function UserAccess({ collection, relationBound }: { collection: CollectionDefinition; relationBound: boolean }) {
  const { schema } = useBackend();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [loadingFailed, setLoadingFailed] = useState(false);
  const [updateFailed, setUpdateFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);
  const request = useRef<AbortController | null>(null);
  const pending = useRef(false);

  const load = useCallback(() => {
    if (pending.current) return;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const issued = generation.current;
    const current = () => !controller.signal.aborted && issued === generation.current;
    setEnabled(null);
    setLoadingFailed(false);
    void schema.userAccess(collection.name, controller.signal).then((value) => {
      if (current()) setEnabled(value.enabled);
    }).catch(() => {
      if (current()) setLoadingFailed(true);
    });
  }, [schema, collection.name]);

  useEffect(() => {
    generation.current += 1;
    pending.current = false;
    setBusy(false);
    setUpdateFailed(false);
    if (!relationBound) load();
    return () => {
      generation.current += 1;
      request.current?.abort();
    };
  }, [load, relationBound]);

  async function toggle() {
    if (enabled === null || pending.current || relationBound) return;
    pending.current = true;
    const issued = generation.current;
    setBusy(true);
    setUpdateFailed(false);
    try {
      const policy = await schema.setUserAccess(collection.name, !enabled);
      if (issued === generation.current) setEnabled(policy.enabled);
    } catch {
      if (issued === generation.current) setUpdateFailed(true);
    } finally {
      if (issued === generation.current) {
        pending.current = false;
        setBusy(false);
      }
    }
  }

  if (relationBound) return <span>Public user records are blocked while this collection has incoming or outgoing relations.</span>;
  if (loadingFailed) return <span role="alert">Unable to load user access. <button type="button" onClick={() => load()}>Retry user access</button></span>;
  return <span>
    <button type="button" disabled={busy || enabled === null} onClick={() => void toggle()}>{enabled ? "Disable user access" : "Enable user access"}</button>
    {updateFailed && <span role="alert"> Unable to update user access.</span>}
  </span>;
}

export function CollectionsPage({ navigate }: CollectionsPageProps) {
  const { schema } = useBackend();
  const [catalog, setCatalog] = useState<CollectionCatalog | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    setCatalog(null);
    setFailed(false);
    void schema.catalog(controller.signal).then((result) => {
      if (!controller.signal.aborted) {
        setCatalog(result);
        setFailed(false);
      }
    }).catch(() => {
      if (!controller.signal.aborted) setFailed(true);
    });
    return () => controller.abort();
  }, [schema]);

  const collections = catalog?.all() ?? [];
  const relationTargets = new Set(collections.flatMap((collection) =>
    collection.fields.flatMap((field) => field.relation ? [field.relation.targetCollectionId] : []),
  ));
  return <section>
      <div className="page-heading">
      <div><h2>Collections</h2>{catalog && <p>Catalog version {catalog.version}</p>}</div>
      <span><AdminLink className="link-button" navigate={navigate} route={{ kind: "collection-create" }}>Create collection</AdminLink> <AdminLink className="link-button" navigate={navigate} route={{ kind: "collection-scaffold", scaffold: "base" }}>Base scaffold</AdminLink> <AdminLink className="link-button" navigate={navigate} route={{ kind: "collection-scaffold", scaffold: "auth" }}>Auth scaffold</AdminLink> <AdminLink className="link-button" navigate={navigate} route={{ kind: "collection-scaffold", scaffold: "view" }}>View scaffold</AdminLink></span>
    </div>
    {failed && <p role="alert">Unable to load collections.</p>}
    {!catalog && !failed && <p aria-live="polite">Loading collections…</p>}
    {catalog && collections.length === 0 && <p>No collections yet.</p>}
    {catalog && <ul className="collection-list">
      {collections.map((collection) => {
        const relationCount = collection.fields.filter((field) => field.type === "relation").length;
        return <li key={collection.name}>
          <AdminLink className="text-button" navigate={navigate} route={{ kind: "records", collection: collection.name }}>{collection.name}</AdminLink>
          <span>{collection.fields.length} fields{relationCount > 0 ? ` · ${relationCount} ${relationCount === 1 ? "relation" : "relations"}` : ""}</span>
          <UserAccess collection={collection} relationBound={relationCount > 0 || (collection.id !== undefined && relationTargets.has(collection.id))} />
          <AdminLink className="text-button" navigate={navigate} route={{ kind: "collection-edit", collection: collection.name }}>Edit</AdminLink>
          <AdminLink className="text-button" navigate={navigate} route={{ kind: "collection-duplicate", collection: collection.name }}>Duplicate</AdminLink>
          <TruncateCollection collection={collection} />
        </li>;
      })}
    </ul>}
  </section>;
}
