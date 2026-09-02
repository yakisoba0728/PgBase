import { useEffect, useMemo, useState } from "react";
import { APIClientError } from "../api/client";
import { stringifyJSON } from "../api/json";
import { collectionFromCatalog, fieldTypes, type CollectionCatalog, type CollectionChangeInput, type CollectionDefinition, type CollectionPlan } from "../api/collections";
import { useSession } from "../auth/session";
import { adminRouteIdentity, useRouteGuard, type AdminRoute } from "../routing/admin-location";
import {
  buildInput,
  CollectionFieldEditor,
  draftErrorMessage,
  draftFieldsFromDefinition,
  newDraftField,
  previewField,
  relationTargetOptions,
  schemaErrorMessage,
  type DraftField,
} from "./collection-field-editor";

function toInput(definition: CollectionDefinition, fields: DraftField[]): CollectionChangeInput {
  return buildInput("update", definition.name, fields);
}

export function CollectionEditPage({ collectionName, navigate }: { collectionName: string; navigate(route: AdminRoute): void }) {
  const { collections } = useSession();
  const [catalog, setCatalog] = useState<CollectionCatalog | null>(null);
  const [definition, setDefinition] = useState<CollectionDefinition | null>(null);
  const [fields, setFields] = useState<DraftField[]>([]);
  const [plan, setPlan] = useState<CollectionPlan | null>(null);
  const [plannedInput, setPlannedInput] = useState<CollectionChangeInput | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);
  const [destructiveConfirmed, setDestructiveConfirmed] = useState(false);

  // Delete flow state: null → no delete in progress; otherwise the SQL preview is shown.
  const [deletePlan, setDeletePlan] = useState<CollectionPlan | null>(null);
  const [deletePending, setDeletePending] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const active = useRouteGuard(adminRouteIdentity({ kind: "collection-edit", collection: collectionName }));

  useEffect(() => {
    const controller = new AbortController();
    setDefinition(null);
    setCatalog(null);
    setFields([]);
    setPlan(null);
    setPlannedInput(null);
    setPending(false);
    setError(null);
    setNotFound(false);
    setLoadFailed(false);
    setDestructiveConfirmed(false);
    setDeletePlan(null);
    setDeletePending(false);
    setDeleteError(null);
    void collections.catalog(controller.signal).then((catalog) => {
      if (controller.signal.aborted) return;
      const existing = collectionFromCatalog(catalog, collectionName);
      if (!existing) { setNotFound(true); return; }
      setCatalog(catalog);
      setDefinition(existing);
      setFields(draftFieldsFromDefinition(existing, catalog));
    }).catch(() => { if (!controller.signal.aborted) setLoadFailed(true); });
    return () => controller.abort();
  }, [collectionName, collections]);

  const inputSignature = useMemo(() => {
    if (definition === null) return "";
    try { return stringifyJSON(toInput(definition, fields)); } catch { return "invalid"; }
  }, [definition, fields]);
  const previewCurrent = plan !== null && plannedInput !== null && stringifyJSON(plannedInput) === inputSignature;
  const relationTargets = useMemo(() => relationTargetOptions(catalog, definition?.name ?? collectionName), [catalog, collectionName, definition?.name]);
  const existingFieldNames = useMemo(() => new Set(definition?.fields.map((field) => field.name) ?? []), [definition]);
  const removedFields = useMemo(() => {
    if (!previewCurrent || definition === null || plan === null) return [];
    const plannedNames = new Set(plan.definition.fields.map((field) => field.name));
    return definition.fields
      .filter((field) => !plannedNames.has(field.name))
      .sort((left, right) => left.ordinal - right.ordinal)
      .map((field) => field.name);
  }, [definition, plan, previewCurrent]);

  function resetPlan() {
    setPlan(null);
    setPlannedInput(null);
    setDestructiveConfirmed(false);
  }

  function changeField(index: number, field: DraftField) {
    resetPlan();
    setFields((current) => current.map((currentField, position) => position === index ? field : currentField));
  }

  function addField() {
    resetPlan();
    setFields((current) => [...current, newDraftField()]);
  }

  function removeField(index: number) {
    resetPlan();
    setFields((current) => current.filter((_, position) => position !== index));
  }

  async function preview() {
    if (definition === null || pending) return;
    setError(null);
    let input: CollectionChangeInput;
    try { input = toInput(definition, fields); } catch (caught: unknown) { setError(draftErrorMessage(caught, "Collection input is invalid.")); return; }
    setPending(true);
    try {
      const result = await collections.plan(input);
      if (!active()) return;
      setPlannedInput(input);
      setPlan(result);
      setDestructiveConfirmed(false);
    } catch (caught: unknown) {
      if (!active()) return;
      resetPlan();
      setError(schemaErrorMessage(caught, "Unable to preview the collection change."));
    } finally {
      if (active()) setPending(false);
    }
  }

  async function apply() {
    if (!previewCurrent || plan === null || plannedInput === null || pending || (removedFields.length > 0 && !destructiveConfirmed)) return;
    const appliedBaseVersion = plan.baseVersion;
    setPending(true);
    setError(null);
    try {
      await collections.apply(plannedInput, plan.checksum, plan.baseVersion);
      if (!active()) return;
      navigate({ kind: "records", collection: definition?.name ?? collectionName });
    } catch (caught: unknown) {
      if (!active()) return;
      if (caught instanceof APIClientError && caught.status === 409) {
        resetPlan();
        // A 409 covers a stale plan, a plan that conflicts with existing data
        // (for example a unique index over duplicate values), and object-name
        // collisions. Tell a moved catalog apart from a same-version conflict.
        try {
          const catalog = await collections.catalog();
          if (!active()) return;
          if (catalog.version === appliedBaseVersion) {
            setError("The plan conflicts with the current state. Preview again; if it keeps failing, existing data may be blocking it.");
          } else {
            setError("The catalog changed. Preview the collection again before applying.");
          }
        } catch {
          setError("The catalog changed. Preview the collection again before applying.");
        }
      } else if (caught instanceof APIClientError && caught.status === 422) {
        setError(schemaErrorMessage(caught, "Existing data blocks this change (for example missing or duplicate values).", "Existing data blocks this change (for example missing or duplicate values)."));
      } else setError(schemaErrorMessage(caught, "Unable to apply the collection change."));
    } finally {
      if (active()) setPending(false);
    }
  }

  async function previewDelete() {
    if (definition === null || deletePending) return;
    setDeleteError(null);
    setDeletePending(true);
    try {
      const result = await collections.plan({ operation: "delete", name: definition.name });
      if (!active()) return;
      setDeletePlan(result);
    } catch (caught: unknown) {
      if (!active()) return;
      setDeleteError(schemaErrorMessage(caught, "Unable to preview the collection deletion."));
    } finally {
      if (active()) setDeletePending(false);
    }
  }

  async function applyDelete() {
    if (definition === null || deletePlan === null || deletePending) return;
    setDeletePending(true);
    setDeleteError(null);
    try {
      await collections.apply({ operation: "delete", name: definition.name }, deletePlan.checksum, deletePlan.baseVersion);
      if (!active()) return;
      navigate({ kind: "collections" });
    } catch (caught: unknown) {
      if (!active()) return;
      setDeletePlan(null);
      if (caught instanceof APIClientError && caught.code === "collection_referenced") {
        setDeleteError(schemaErrorMessage(caught, "Unable to delete the collection.", "Collection is referenced."));
      } else if (caught instanceof APIClientError && caught.status === 409) {
        setDeleteError("The catalog changed. Preview the deletion again before applying.");
      } else setDeleteError(schemaErrorMessage(caught, "Unable to delete the collection."));
    } finally {
      if (active()) setDeletePending(false);
    }
  }

  if (notFound) return <section><h2>Edit collection</h2><p role="alert">Collection not found.</p></section>;
  if (loadFailed) return <section><h2>Edit collection</h2><p role="alert">Unable to load collection.</p></section>;
  if (definition === null) return <section><h2>Edit collection</h2><p aria-live="polite">Loading collection…</p></section>;

  return <section>
    <h2>Edit collection</h2>
    <p>{definition.name} · physical table {definition.tableName}</p>
    {error && <p role="alert">{error}</p>}
    <form onSubmit={(event) => { event.preventDefault(); void preview(); }}>
      <fieldset>
        <legend>Fields</legend>
        {fields.map((field, index) => <CollectionFieldEditor
          field={field}
          fieldCount={fields.length}
          fieldTypes={fieldTypes}
          index={index}
          key={index}
          onChange={(next) => changeField(index, next)}
          onRemove={() => removeField(index)}
          pending={pending}
          relationTargets={relationTargets}
          showRequiredRelationMigration={!existingFieldNames.has(field.name)}
        />)}
        <button className="secondary" onClick={addField} type="button">Add field</button>
      </fieldset>
      <button disabled={pending} type="submit">{pending ? "Previewing…" : "Preview change"}</button>
    </form>
    {previewCurrent && plan && <section className="preview" aria-label="Collection change preview">
      <h3>Server preview</h3>
      <p>Catalog version {plan.baseVersion}</p>
      <ul>{plan.definition.fields.map((field) => <li key={field.name}>{previewField(field)}</li>)}</ul>
      <h4>SQL</h4>
      <pre>{plan.sql.join("\n\n")}</pre>
      {removedFields.length > 0 && <div aria-label="Permanent data loss warning" className="danger-zone" role="alert">
        <p>This change permanently deletes data from these fields:</p>
        <ul>{removedFields.map((name) => <li key={name}>{name}</li>)}</ul>
        <label><input checked={destructiveConfirmed} onChange={(event) => setDestructiveConfirmed(event.target.checked)} type="checkbox" /> I understand that these fields and their data will be permanently deleted.</label>
      </div>}
      <button disabled={pending || (removedFields.length > 0 && !destructiveConfirmed)} onClick={() => void apply()} type="button">{pending ? "Applying…" : "Apply change"}</button>
    </section>}
    <section className="danger-zone" aria-label="Danger zone">
      <h3>Danger zone</h3>
      {deleteError && <p role="alert">{deleteError}</p>}
      {deletePlan === null
        ? <button className="danger" disabled={deletePending} onClick={() => void previewDelete()} type="button">{deletePending ? "Preparing…" : "Delete collection"}</button>
        : <div>
          <p>This permanently drops the table and all records.</p>
          <h4>SQL</h4>
          <pre>{deletePlan.sql.join("\n\n")}</pre>
          <button className="danger" disabled={deletePending} onClick={() => void applyDelete()} type="button">{deletePending ? "Deleting…" : "Confirm delete"}</button>
          <button className="secondary" disabled={deletePending} onClick={() => setDeletePlan(null)} type="button">Cancel</button>
        </div>}
    </section>
  </section>;
}
