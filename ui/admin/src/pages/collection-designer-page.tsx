import { previewField, useCollectionDesigner, type DesignerMode } from "../designer/collection-designer";
import type { AdminRoute } from "../routing/admin-location";
import { CollectionFieldEditor } from "./collection-field-editor";

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
    {designer.relationTargetsFailed && <p role="alert">Unable to load relation targets. Reload this page before adding a relation field.</p>}
    <form onSubmit={(event) => { event.preventDefault(); designer.requestPreview(); }}>
      {designer.create && <>
        <label htmlFor="collection-name">Collection name</label>
        <input id="collection-name" onChange={(event) => designer.rename(event.target.value)} required value={designer.name} />
      </>}
      <fieldset>
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
        <button className="secondary" onClick={designer.addField} type="button">Add field</button>
      </fieldset>
      <button disabled={designer.pending} type="submit">{designer.pending ? "Previewing…" : designer.create ? "Preview collection" : "Preview change"}</button>
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
      {designer.deletePlan === null
        ? <button className="danger" disabled={designer.deletePending} onClick={designer.requestDeletePlan} type="button">{designer.deletePending ? "Preparing…" : "Delete collection"}</button>
        : <div>
          <p>This permanently drops the table and all records.</p>
          <h4>SQL</h4>
          <pre>{designer.deletePlan.sql.join("\n\n")}</pre>
          <button className="danger" disabled={designer.deletePending} onClick={designer.requestDelete} type="button">{designer.deletePending ? "Deleting…" : "Confirm delete"}</button>
          <button className="secondary" disabled={designer.deletePending} onClick={designer.cancelDelete} type="button">Cancel</button>
        </div>}
    </section>}
  </section>;
}
