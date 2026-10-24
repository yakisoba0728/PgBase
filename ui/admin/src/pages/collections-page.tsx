import { useCallback, useEffect, useRef, useState } from "react";
import type { CollectionCatalog, CollectionDefinition } from "../backend/ports";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { useBackend } from "../auth/session";

import { Button, EmptyState, PageHeading } from "../components/ui";
import { WorkspaceIcon } from "../components/workspace-icon";

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

function UserAccess({ collection }: { collection: CollectionDefinition }) {
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
    load();
    return () => {
      generation.current += 1;
      request.current?.abort();
    };
  }, [load]);

  async function toggle() {
    if (enabled === null || pending.current) return;
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

  if (loadingFailed) return <span role="alert">Unable to load user access. <Button variant="secondary" onClick={() => load()}>Retry user access</Button></span>;
  return <span>
    <Button variant="secondary" disabled={busy || enabled === null} onClick={() => void toggle()}>{enabled ? "Disable user access" : "Enable user access"}</Button>
    {updateFailed && <span role="alert"> Unable to update user access.</span>}
  </span>;
}

export function CollectionsPage({ navigate }: CollectionsPageProps) {
  const { schema } = useBackend();
  const [catalog, setCatalog] = useState<CollectionCatalog | null>(null);
  const [failed, setFailed] = useState(false);
  const [search, setSearch] = useState("");

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
  const visibleCollections = collections.filter(collection => collection.name.toLowerCase().includes(search.trim().toLowerCase()));
  return <section className="collections-page">
    <PageHeading title="Collections" description="Manage schemas and records in your PostgreSQL project." actions={<AdminLink className="link-button" navigate={navigate} route={{ kind: "collection-create" }}><WorkspaceIcon name="plus" />Create collection</AdminLink>} />
    <div className="collection-toolbar"><label className="collection-search"><WorkspaceIcon name="search" /><input aria-label="Search collections" placeholder="Search collections…" type="search" value={search} onChange={event => setSearch(event.target.value)} /></label>{catalog && <span className="catalog-meta">{collections.length} {collections.length === 1 ? "collection" : "collections"}<span className="catalog-divider">/</span><span>Catalog version {catalog.version}</span></span>}</div>
    {failed && <p role="alert">Unable to load collections.</p>}
    {!catalog && !failed && <p aria-live="polite">Loading collections…</p>}
    {catalog && collections.length === 0 && <EmptyState title="No collections yet." icon="database" description={<>A collection gives your records a home.<br />Start with a blank schema, or choose a template below.</>} action={<AdminLink className="text-button empty-create" navigate={navigate} route={{ kind: "collection-create" }}>Create your first collection<WorkspaceIcon name="arrow" /></AdminLink>} />}
    {catalog && collections.length > 0 && visibleCollections.length === 0 && <p className="search-empty" role="status">No collections match “{search}”.</p>}
    {catalog && visibleCollections.length > 0 && <ul className="collection-list" aria-label="Collections">
      {visibleCollections.map((collection) => {
        const relationCount = collection.fields.filter((field) => field.type === "relation").length;
        return <li key={collection.name}>
          <div className="collection-identity"><span className="collection-symbol"><WorkspaceIcon name={collection.view ? "layers" : "database"} /></span><div><AdminLink className="text-button collection-name" navigate={navigate} route={{ kind: "records", collection: collection.name }}>{collection.name}</AdminLink><span className="collection-kind">{collection.view ? "View collection" : collection.type === "auth" ? "Auth collection" : "Base collection"}</span></div></div>
          <span className="collection-metadata">{collection.fields.length} fields{relationCount > 0 ? ` · ${relationCount} ${relationCount === 1 ? "relation" : "relations"}` : ""}</span>
          <div className="collection-row-actions"><UserAccess collection={collection} />
          <AdminLink className="text-button" navigate={navigate} route={{ kind: "collection-edit", collection: collection.name }}>Edit</AdminLink>
          <AdminLink className="text-button" navigate={navigate} route={{ kind: "collection-duplicate", collection: collection.name }}>Duplicate</AdminLink>
          <TruncateCollection collection={collection} /></div>
        </li>;
      })}
    </ul>}
    {catalog && <div className="collection-templates"><div className="template-heading"><h3>Start with a template</h3><p>A starting point for your next collection.</p></div><div className="template-grid">{([
      { kind: "base", label: "Base scaffold", icon: "database", title: "Structured data", description: "Build a schema for any kind of record." },
      { kind: "auth", label: "Auth scaffold", icon: "shield", title: "Authentication", description: "Start with fields for your app’s users." },
      { kind: "view", label: "View scaffold", icon: "layers", title: "SQL view", description: "Explore data with a read-only SQL view." },
    ] as const).map(template => <AdminLink className="template-card" key={template.kind} navigate={navigate} route={{ kind: "collection-scaffold", scaffold: template.kind }}><WorkspaceIcon name={template.icon} /><strong>{template.title}</strong><span>{template.description}</span><span className="template-action">{template.label}<WorkspaceIcon name="arrow" /></span></AdminLink>)}</div></div>}
  </section>;
}
