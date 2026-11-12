import { t } from "../i18n";
import { useEffect, useRef, useState } from "react";
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
  return <div aria-label={t("Schema operation recovery")} className="operation-recovery" role="status">
    {recovery.status === "committed" && <p>{t("The change was applied. Operation")} {recovery.operationId}  {t("committed.")}</p>}
    {recovery.status === "failed" && <p>{t(operationFailureMessage(recovery.failureCode, "The schema operation did not apply."))}</p>}
    {recovery.undecided && <p>
       {t("The change was sent but its outcome is not known yet. It may still be applying — do not send it again until this says whether it was applied. Operation")} {recovery.operationId}.
    </p>}
    {recovery.undecided && !recovery.checking && <button onClick={recovery.recheck} type="button">{t("Check operation status")}</button>}
    <AdminLink className="text-button" navigate={navigate} route={{ kind: "operations" }}>{t("View schema operations")}</AdminLink>
  </div>;
}

export function CollectionDesignerPage({ mode, navigate }: { mode: DesignerMode; navigate(route: AdminRoute): void }) {
  const { schema } = useBackend();
  const [tab, setTab] = useState("fields");
  const [rulesDirty, setRulesDirty] = useState(false);
  const designer = useCollectionDesigner(mode, navigate);
  const preview = useRef<HTMLElement>(null);
  useEffect(() => { if (designer.preview) { preview.current?.focus({ preventScroll: true }); preview.current?.scrollIntoView?.({ block: "start" }); } }, [designer.preview]);
  const heading = mode.kind === "duplicate" ? t("Duplicate collection") : mode.kind === "scaffold" ? t("Create {kind} scaffold", { kind: t(mode.scaffold) }) : designer.create ? t("Create collection") : t("Edit collection");
  const pageHeader = <><BackLink navigate={navigate} route={{ kind: "collections" }}>{t("Back to collections")}</BackLink><PageHeading title={heading} description={designer.create
    ? mode.kind === "duplicate" ? t("The duplicate starts with the source schema and creates a separate empty collection.") : t("Preview the server-generated schema plan before applying it.")
    : designer.phase === "ready" ? t("{name} · physical table {table}", { name: designer.name, table: designer.tableName }) : undefined} /></>;

  if (designer.phase === "missing") return <section className="collection-designer">{pageHeader}<p role="alert">{t("Collection not found.")}</p></section>;
  if (designer.phase === "unavailable") return <section className="collection-designer">{pageHeader}<p role="alert">{t("Unable to load collection.")}</p></section>;
  if (designer.phase === "loading") return <section className="collection-designer">{pageHeader}<p aria-live="polite">{t("Loading collection…")}</p></section>;

  return <section className="collection-designer">
    {pageHeader}
    {designer.error && <p role="alert">{t(designer.error)}</p>}
    {designer.recovery !== null && <ApplyRecovery navigate={navigate} recovery={designer.recovery} />}
    {designer.relationTargetsFailed && <p role="alert">{t("Unable to load relation targets. Reload this page before adding a relation field.")}</p>}
    {designer.isView && <p>{designer.isSQLView ? t("Records in this SQL view are read-only. You can edit its SQL definition.") : t("Records in this view are read-only. You can edit its source, projected fields, and static filter.")}</p>}
    <form onInvalidCapture={event => { const panel = (event.target as HTMLElement).closest('[role="tabpanel"]'); if (panel) setTab(panel.id.replace("designer-panel-", "")); }} onSubmit={(event) => { event.preventDefault(); if (tab !== "rules") designer.requestPreview(); }}>
      <div className="collection-basics">
      {designer.create && <div className="form-control">
        <label htmlFor="collection-name">{t("Collection name")}</label>
        <input aria-describedby="collection-name-help" autoComplete="off" disabled={designer.editingBlocked} id="collection-name" onChange={(event) => designer.rename(event.target.value)} required value={designer.name} />
        <span className="control-help" id="collection-name-help">{t("Used in record and API paths.")}</span>
      </div>}
      {designer.create && designer.collectionType === "auth" && <p>{t("Collection kind: Authentication realm. System fields and authentication policy are preserved in the server-owned template.")}</p>}
      {designer.create && designer.collectionType !== "auth" && <label>{t("Collection kind")} <select disabled={designer.editingBlocked} value={designer.view === undefined ? "base" : "view"} onChange={(event) => { designer.setView(event.target.value === "base" ? undefined : { source: "", projection: [] }); setTab("fields"); }}><option value="base">{t("Base collection")}</option><option value="view">{t("Read-only view")}</option></select></label>}
      </div>
      <div className="designer-tabs" role="tablist" aria-label={t("Collection settings")}>{[
        ["fields", designer.isView ? t("View definition") : t("Fields")],
        ...(!designer.create ? [["rules", "API Rules"]] : []),
        ...(!designer.isView ? [["indexes", t("Indexes")]] : []), ["options", t("Options")],
      ].map(([id, label]) => <button type="button" role="tab" key={id} id={`designer-tab-${id}`} aria-controls={`designer-panel-${id}`} aria-selected={tab === id} tabIndex={tab === id ? 0 : -1} onClick={() => setTab(id)} onKeyDown={event => {
        if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
        event.preventDefault();
        const tabs = Array.from(event.currentTarget.parentElement!.querySelectorAll<HTMLButtonElement>('[role="tab"]'));
        const index = tabs.indexOf(event.currentTarget);
        const next = tabs[event.key === "Home" ? 0 : event.key === "End" ? tabs.length - 1 : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) % tabs.length];
        next.click(); next.focus();
      }}>{t(label)}</button>)}</div>
      <div className="designer-panel" role="tabpanel" id="designer-panel-fields" aria-labelledby="designer-tab-fields" hidden={tab !== "fields"}>

		{designer.view !== undefined ? <ViewDefinitionEditor view={designer.view} sources={designer.viewSources} disabled={designer.editingBlocked} modeLocked={!designer.create} onChange={designer.setView} schema={schema} /> : <>
      <fieldset className="collection-fields" disabled={designer.editingBlocked}>
        <legend>{t("Fields")}</legend>
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
        <Button variant="secondary" disabled={designer.editingBlocked} onClick={designer.addField}>{t("Add field")}</Button>
      </fieldset>
      </>}
      </div>
      {!designer.isView && <div className="designer-panel" role="tabpanel" id="designer-panel-indexes" aria-labelledby="designer-tab-indexes" hidden={tab !== "indexes"}>
      <h3>{t("Indexes")}</h3><p className="control-help">{t("Speed up lookups across multiple fields, or enforce a unique combination.")}</p>
      <fieldset className="collection-indexes" disabled={designer.editingBlocked}><legend>{t("Compound indexes")}</legend>
        {designer.indexes.map((index, position) => <div key={position}>
          <label>{t("Index name")} <input value={index.name} onChange={(event) => designer.setIndexes(designer.indexes.map((entry, i) => i === position ? { ...entry, name: event.target.value } : entry))} /></label>
          <label>{t("Ordered fields")} <input aria-label={t("Index {number} fields", { number: position + 1 })} value={index.fields.join(", ")} onChange={(event) => designer.setIndexes(designer.indexes.map((entry, i) => i === position ? { ...entry, fields: event.target.value.split(",").map((field) => field.trim()) } : entry))} /></label>
          <label><input checked={index.unique} type="checkbox" onChange={(event) => designer.setIndexes(designer.indexes.map((entry, i) => i === position ? { ...entry, unique: event.target.checked } : entry))} />  {t("Unique")}</label>
          <button type="button" onClick={() => designer.setIndexes(designer.indexes.filter((_, i) => i !== position))}>{t("Remove index")}</button>
        </div>)}
        <Button variant="secondary" onClick={() => designer.setIndexes([...designer.indexes, { name: "", fields: [], unique: false }])}>{t("Add compound index")}</Button>
        <p>{t("Each compound index needs two to eight ordered scalar fields.")}</p>
      </fieldset>
      </div>}
      <div className="designer-panel" role="tabpanel" id="designer-panel-options" aria-labelledby="designer-tab-options" hidden={tab !== "options"}>
      <details className="advanced-options"><summary>{t("Advanced collection options")}</summary><fieldset disabled={designer.editingBlocked} className="settings-field-grid">
        <label>{t("Compatibility name")}<input value={designer.advanced?.compatibilityName ?? ""} onChange={e => designer.setAdvanced({ ...designer.advanced, compatibilityName: e.target.value })} /><span className="settings-help">{t("Optional alternate collection name for compatible API clients. Leave empty to use the collection name.")}</span></label>
        <label>{t("Validation mode")}<select value={designer.advanced?.validationMode ?? "postgres"} onChange={e => designer.setAdvanced({ ...designer.advanced, validationMode: e.target.value as "postgres" | "pocketbase" })}><option value="postgres">{t("PostgreSQL")}</option><option value="pocketbase">{t("PocketBase compatible")}</option></select><span className="settings-help">{t("Changes are included in the server preview before applying.")}</span></label>
      </fieldset></details>
      {designer.view === undefined && <RecordIDPolicyEditor value={designer.recordId} locked={!designer.create} disabled={designer.editingBlocked} onChange={designer.setRecordIDPolicy} />}
      </div>
      {!designer.create && <div className="designer-panel" role="tabpanel" id="designer-panel-rules" aria-labelledby="designer-tab-rules" hidden={tab !== "rules"}><RecordRulesEditor collection={designer.name} onDirty={setRulesDirty} /></div>}
      {tab !== "rules" && <div className="designer-actions"><Button disabled={designer.pending || designer.editingBlocked} type="submit">{designer.pending ? t("Previewing…") : designer.create ? t("Preview collection") : t("Preview change")}</Button><span className="control-help">{t("Review the changes before applying them.")}</span></div>}
    </form>
    {designer.preview && <section ref={preview} tabIndex={-1} className="preview" aria-label={designer.create ? t("Collection preview") : t("Collection change preview")}>
      <h3>{t("Server preview")}</h3>
      <p>{t("Catalog version")} {designer.preview.baseVersion}</p>
      <ul>{designer.preview.fields.map((field) => <li key={field.name}>{previewField(field)}</li>)}</ul>
      <h4>{t("SQL")}</h4>
      <pre>{(designer.preview.sql ?? []).join("\n\n") || t("No SQL statements.")}</pre>
      {designer.removedFields.length > 0 && <div aria-label={t("Permanent data loss warning")} className="danger-zone" role="alert">
        <p>{t("This change permanently deletes data from these fields:")}</p>
        <ul>{designer.removedFields.map((name) => <li key={name}>{name}</li>)}</ul>
        <label><input checked={designer.destructiveConfirmed} onChange={(event) => designer.confirmDestructive(event.target.checked)} type="checkbox" />  {t("I understand that these fields and their data will be permanently deleted.")}</label>
      </div>}
      <button disabled={designer.pending || designer.applyBlocked} onClick={designer.requestApply} type="button">
        {designer.pending ? t("Applying…") : designer.create ? t("Apply collection") : t("Apply change")}
      </button>
    </section>}
    {!designer.create && <section hidden={tab !== "options"} className="danger-zone" aria-label={t("Danger zone")}>
      <h3>{t("Danger zone")}</h3>
      {rulesDirty && <p role="status">{t("Save or reset your API Rules changes before deleting this collection.")}</p>}
      {designer.deleteError && <p role="alert">{t(designer.deleteError)}</p>}
      {designer.deleteTarget === null
        ? <button className="danger" disabled={designer.deletePending || designer.editingBlocked || rulesDirty} onClick={designer.requestDeletePlan} type="button">{designer.deletePending ? t("Preparing…") : t("Delete collection")}</button>
        : <div>
          <p>{designer.isView ? t("This permanently drops the view; source records remain.") : t("This permanently drops the table and all records.")}</p>
          <p>{t("Deleting {name} · physical table {table} · catalog version {version}", { name: designer.deleteTarget.collection, table: designer.deleteTarget.tableName, version: designer.deleteTarget.baseVersion })}</p>
          <h4>{t("SQL")}</h4>
          <pre>{designer.deleteTarget.sql.join("\n\n")}</pre>
          <label htmlFor="delete-confirmation">{t("Type {name} to confirm", { name: designer.deleteTarget.collection })}</label>
          <input autoComplete="off" disabled={designer.editingBlocked} id="delete-confirmation" onChange={(event) => designer.confirmDeleteName(event.target.value)} value={designer.deleteConfirmText} />
          <button className="danger" disabled={designer.deletePending || designer.editingBlocked || rulesDirty || !designer.deleteConfirmed} onClick={designer.requestDelete} type="button">{designer.deletePending ? t("Deleting…") : t("Confirm delete")}</button>
          <button className="secondary" disabled={designer.deletePending || designer.editingBlocked} onClick={designer.cancelDelete} type="button">{t("Cancel")}</button>
        </div>}
    </section>}
  </section>;
}
