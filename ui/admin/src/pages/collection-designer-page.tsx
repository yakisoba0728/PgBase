import { useEffect, useRef } from "react";
import { previewField, useCollectionDesigner, type DesignerMode } from "../designer/collection-designer";
import { operationFailureMessage, type OperationRecovery } from "../operations/operation-recovery";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { CollectionFieldEditor } from "./collection-field-editor";
import { ViewDefinitionEditor } from "./view-definition-editor";
import { RecordRulesEditor } from "./record-rules-editor";
import { RecordIDPolicyEditor } from "./record-id-policy-editor";
import { useBackend } from "../auth/session";
import { BackLink, Button, PageHeading } from "../components/ui";

/**
 * The outcome of an apply the server could not decide within the request. It
 * reports "not yet known" for as long as that is the truth: the administrator
 * is never shown a success or a failure the ledger has not confirmed.
 */
function ApplyRecovery({ navigate, recovery }: { navigate(route: AdminRoute): void; recovery: OperationRecovery }) {
  return <div aria-label="Schema operation recovery" className="operation-recovery" role="status">
    {recovery.status === "committed" && <p>The change was applied. Operation {recovery.operationId} committed.</p>}
    {recovery.status === "failed" && <p>{operationFailureMessage(recovery.failureCode, "The schema operation did not apply.")}</p>}
    {recovery.undecided && <p>
      The change was sent but its outcome is not known yet. It may still be applying — do not send it again until this says
      whether it was applied. Operation {recovery.operationId}.
    </p>}
    {recovery.undecided && !recovery.checking && <button onClick={recovery.recheck} type="button">Check operation status</button>}
    <AdminLink className="text-button" navigate={navigate} route={{ kind: "operations" }}>View schema operations</AdminLink>
  </div>;
}

export function CollectionDesignerPage({ mode, navigate }: { mode: DesignerMode; navigate(route: AdminRoute): void }) {
  const { schema } = useBackend();
  const designer = useCollectionDesigner(mode, navigate);
  const preview = useRef<HTMLElement>(null);
  useEffect(() => { if (designer.preview) { preview.current?.focus({ preventScroll: true }); preview.current?.scrollIntoView?.({ block: "start" }); } }, [designer.preview]);
  const heading = mode.kind === "duplicate" ? "Duplicate collection" : mode.kind === "scaffold" ? `Create ${mode.scaffold} scaffold` : designer.create ? "Create collection" : "Edit collection";
  const pageHeader = <><BackLink navigate={navigate} route={{ kind: "collections" }}>Back to collections</BackLink><PageHeading title={heading} description={designer.create
    ? mode.kind === "duplicate" ? "The duplicate starts with the source schema and creates a separate empty collection." : "Preview the server-generated schema plan before applying it."
    : designer.phase === "ready" ? `${designer.name} · physical table ${designer.tableName}` : undefined} /></>;

  if (designer.phase === "missing") return <section className="collection-designer">{pageHeader}<p role="alert">Collection not found.</p></section>;
  if (designer.phase === "unavailable") return <section className="collection-designer">{pageHeader}<p role="alert">Unable to load collection.</p></section>;
  if (designer.phase === "loading") return <section className="collection-designer">{pageHeader}<p aria-live="polite">Loading collection…</p></section>;

  return <section className="collection-designer">
    {pageHeader}
    {designer.error && <p role="alert">{designer.error}</p>}
    {designer.recovery !== null && <ApplyRecovery navigate={navigate} recovery={designer.recovery} />}
    {designer.relationTargetsFailed && <p role="alert">Unable to load relation targets. Reload this page before adding a relation field.</p>}
    {designer.isView && <p>{designer.isSQLView ? "Records in this SQL view are read-only. You can edit its SQL definition." : "Records in this view are read-only. You can edit its source, projected fields, and static filter."}</p>}
    <form onSubmit={(event) => { event.preventDefault(); designer.requestPreview(); }}>
      <div className="collection-basics">
      {designer.create && <div className="form-control">
        <label htmlFor="collection-name">Collection name</label>
        <input aria-describedby="collection-name-help" autoComplete="off" disabled={designer.editingBlocked} id="collection-name" onChange={(event) => designer.rename(event.target.value)} required value={designer.name} />
        <span className="control-help" id="collection-name-help">Used in record and API paths.</span>
      </div>}
      {designer.create && designer.collectionType === "auth" && <p>Collection kind: Authentication realm. System fields and authentication policy are preserved in the server-owned template.</p>}
      {designer.create && designer.collectionType !== "auth" && <label>Collection kind <select disabled={designer.editingBlocked} value={designer.view === undefined ? "base" : "view"} onChange={(event) => designer.setView(event.target.value === "base" ? undefined : { source: "", projection: [] })}><option value="base">Base collection</option><option value="view">Read-only view</option></select></label>}
      </div>
      <details className="advanced-options"><summary>Advanced collection options</summary><fieldset disabled={designer.editingBlocked} className="settings-field-grid">
        <label>Compatibility name<input value={designer.advanced?.compatibilityName ?? ""} onChange={e => designer.setAdvanced({ ...designer.advanced, compatibilityName: e.target.value })} /><span className="settings-help">Optional alternate collection name for compatible API clients. Leave empty to use the collection name.</span></label>
        <label>Validation mode<select value={designer.advanced?.validationMode ?? "postgres"} onChange={e => designer.setAdvanced({ ...designer.advanced, validationMode: e.target.value as "postgres" | "pocketbase" })}><option value="postgres">PostgreSQL</option><option value="pocketbase">PocketBase compatible</option></select><span className="settings-help">Changes are included in the server preview before applying.</span></label>
      </fieldset></details>
      {designer.view === undefined && <RecordIDPolicyEditor value={designer.recordId} locked={!designer.create} disabled={designer.editingBlocked} onChange={designer.setRecordIDPolicy} />}
		{designer.view !== undefined ? <ViewDefinitionEditor view={designer.view} sources={designer.viewSources} disabled={designer.editingBlocked} modeLocked={!designer.create} onChange={designer.setView} schema={schema} /> : <>
      <fieldset className="collection-fields" disabled={designer.editingBlocked}>
        <legend>Fields</legend>
        {designer.fields.map((field, index) => <CollectionFieldEditor
          field={field}
          fieldCount={designer.fields.length}
          index={index}
          key={index}
          onEdit={(edit) => designer.editField(index, edit)}
          onRemove={() => designer.removeField(index)}
          pending={designer.pending}
          relationTargets={designer.relationTargets}
          relationTargetsLoading={designer.relationTargetsLoading}
          showRequiredRelationMigration={designer.isNewField(field.name)}
        />)}
        <Button variant="secondary" disabled={designer.editingBlocked} onClick={designer.addField}>Add field</Button>
      </fieldset>
      <fieldset className="collection-indexes" disabled={designer.editingBlocked}><legend>Compound indexes</legend>
        {designer.indexes.map((index, position) => <div key={position}>
          <label>Index name <input value={index.name} onChange={(event) => designer.setIndexes(designer.indexes.map((entry, i) => i === position ? { ...entry, name: event.target.value } : entry))} /></label>
          <label>Ordered fields <input aria-label={`Index ${position + 1} fields`} value={index.fields.join(", ")} onChange={(event) => designer.setIndexes(designer.indexes.map((entry, i) => i === position ? { ...entry, fields: event.target.value.split(",").map((field) => field.trim()).filter(Boolean) } : entry))} /></label>
          <label><input checked={index.unique} type="checkbox" onChange={(event) => designer.setIndexes(designer.indexes.map((entry, i) => i === position ? { ...entry, unique: event.target.checked } : entry))} /> Unique</label>
          <button type="button" onClick={() => designer.setIndexes(designer.indexes.filter((_, i) => i !== position))}>Remove index</button>
        </div>)}
        <Button variant="secondary" onClick={() => designer.setIndexes([...designer.indexes, { name: "", fields: [], unique: false }])}>Add compound index</Button>
        <p>Each compound index needs two to eight ordered scalar fields.</p>
      </fieldset>
      </>}
      <div className="designer-actions"><Button disabled={designer.pending || designer.editingBlocked} type="submit">{designer.pending ? "Previewing…" : designer.create ? "Preview collection" : "Preview change"}</Button><span className="control-help">Review the changes before applying them.</span></div>
    </form>
    {designer.preview && <section ref={preview} tabIndex={-1} className="preview" aria-label={designer.create ? "Collection preview" : "Collection change preview"}>
      <h3>Server preview</h3>
      <p>Catalog version {designer.preview.baseVersion}</p>
      <ul>{designer.preview.fields.map((field) => <li key={field.name}>{previewField(field)}</li>)}</ul>
      <h4>SQL</h4>
      <pre>{(designer.preview.sql ?? []).join("\n\n") || "No SQL statements."}</pre>
      {designer.removedFields.length > 0 && <div aria-label="Permanent data loss warning" className="danger-zone" role="alert">
        <p>This change permanently deletes data from these fields:</p>
        <ul>{designer.removedFields.map((name) => <li key={name}>{name}</li>)}</ul>
        <label><input checked={designer.destructiveConfirmed} onChange={(event) => designer.confirmDestructive(event.target.checked)} type="checkbox" /> I understand that these fields and their data will be permanently deleted.</label>
      </div>}
      <button disabled={designer.pending || designer.applyBlocked} onClick={designer.requestApply} type="button">
        {designer.pending ? "Applying…" : designer.create ? "Apply collection" : "Apply change"}
      </button>
    </section>}
    {!designer.create && <RecordRulesEditor collection={designer.name} />}
    {!designer.create && <section className="danger-zone" aria-label="Danger zone">
      <h3>Danger zone</h3>
      {designer.deleteError && <p role="alert">{designer.deleteError}</p>}
      {designer.deleteTarget === null
        ? <button className="danger" disabled={designer.deletePending || designer.editingBlocked} onClick={designer.requestDeletePlan} type="button">{designer.deletePending ? "Preparing…" : "Delete collection"}</button>
        : <div>
          <p>{designer.isView ? "This permanently drops the view; source records remain." : "This permanently drops the table and all records."}</p>
          <p>Deleting {designer.deleteTarget.collection} · physical table {designer.deleteTarget.tableName} · catalog version {designer.deleteTarget.baseVersion}</p>
          <h4>SQL</h4>
          <pre>{designer.deleteTarget.sql.join("\n\n")}</pre>
          <label htmlFor="delete-confirmation">Type {designer.deleteTarget.collection} to confirm</label>
          <input autoComplete="off" disabled={designer.editingBlocked} id="delete-confirmation" onChange={(event) => designer.confirmDeleteName(event.target.value)} value={designer.deleteConfirmText} />
          <button className="danger" disabled={designer.deletePending || designer.editingBlocked || !designer.deleteConfirmed} onClick={designer.requestDelete} type="button">{designer.deletePending ? "Deleting…" : "Confirm delete"}</button>
          <button className="secondary" disabled={designer.deletePending || designer.editingBlocked} onClick={designer.cancelDelete} type="button">Cancel</button>
        </div>}
    </section>}
  </section>;
}
