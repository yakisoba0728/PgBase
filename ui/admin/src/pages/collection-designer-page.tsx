import { previewField, useCollectionDesigner, type DesignerMode } from "../designer/collection-designer";
import { operationFailureMessage, type OperationRecovery } from "../operations/operation-recovery";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";
import { CollectionFieldEditor } from "./collection-field-editor";

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
  const designer = useCollectionDesigner(mode, navigate);
  const heading = designer.create ? "Create collection" : "Edit collection";

  if (designer.phase === "missing") return <section><h2>{heading}</h2><p role="alert">Collection not found.</p></section>;
  if (designer.phase === "unavailable") return <section><h2>{heading}</h2><p role="alert">Unable to load collection.</p></section>;
  if (designer.phase === "loading") return <section><h2>{heading}</h2><p aria-live="polite">Loading collection…</p></section>;

  return <section>
    <h2>{heading}</h2>
    {designer.create
      ? <p>Preview the server-generated schema plan before applying it.</p>
      : <p>{designer.name} · physical table {designer.tableName}</p>}
    {designer.error && <p role="alert">{designer.error}</p>}
    {designer.recovery !== null && <ApplyRecovery navigate={navigate} recovery={designer.recovery} />}
    {designer.relationTargetsFailed && <p role="alert">Unable to load relation targets. Reload this page before adding a relation field.</p>}
    <form onSubmit={(event) => { event.preventDefault(); designer.requestPreview(); }}>
      {designer.create && <>
        <label htmlFor="collection-name">Collection name</label>
        <input disabled={designer.editingBlocked} id="collection-name" onChange={(event) => designer.rename(event.target.value)} required value={designer.name} />
      </>}
      <fieldset disabled={designer.editingBlocked}>
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
        <button className="secondary" disabled={designer.editingBlocked} onClick={designer.addField} type="button">Add field</button>
      </fieldset>
      <button disabled={designer.pending || designer.editingBlocked} type="submit">{designer.pending ? "Previewing…" : designer.create ? "Preview collection" : "Preview change"}</button>
    </form>
    {designer.preview && <section className="preview" aria-label={designer.create ? "Collection preview" : "Collection change preview"}>
      <h3>Server preview</h3>
      <p>Catalog version {designer.preview.baseVersion}</p>
      <ul>{designer.preview.fields.map((field) => <li key={field.name}>{previewField(field)}</li>)}</ul>
      <h4>SQL</h4>
      <pre>{designer.preview.sql.join("\n\n")}</pre>
      {designer.removedFields.length > 0 && <div aria-label="Permanent data loss warning" className="danger-zone" role="alert">
        <p>This change permanently deletes data from these fields:</p>
        <ul>{designer.removedFields.map((name) => <li key={name}>{name}</li>)}</ul>
        <label><input checked={designer.destructiveConfirmed} onChange={(event) => designer.confirmDestructive(event.target.checked)} type="checkbox" /> I understand that these fields and their data will be permanently deleted.</label>
      </div>}
      <button disabled={designer.pending || designer.applyBlocked} onClick={designer.requestApply} type="button">
        {designer.pending ? "Applying…" : designer.create ? "Apply collection" : "Apply change"}
      </button>
    </section>}
    {!designer.create && <section className="danger-zone" aria-label="Danger zone">
      <h3>Danger zone</h3>
      {designer.deleteError && <p role="alert">{designer.deleteError}</p>}
      {designer.deleteTarget === null
        ? <button className="danger" disabled={designer.deletePending || designer.editingBlocked} onClick={designer.requestDeletePlan} type="button">{designer.deletePending ? "Preparing…" : "Delete collection"}</button>
        : <div>
          <p>This permanently drops the table and all records.</p>
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
