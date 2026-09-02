import { useEffect, useMemo, useState } from "react";
import { fieldTypes, SchemaError, type CollectionCatalog, type CollectionChange, type PlannedChange } from "../backend/ports";
import { useBackend } from "../auth/session";
import { adminRouteIdentity, useRouteGuard, type AdminRoute } from "../routing/admin-location";
import {
  buildInput,
  CollectionFieldEditor,
  draftErrorMessage,
  newDraftField,
  previewField,
  relationTargetOptions,
  schemaErrorMessage,
  type DraftField,
} from "./collection-field-editor";

// The shared field editor does not know which collection is being created, so
// this page records whether a relation target was picked as the "(self)" option
// at selection time. Re-deriving self-ness from `targetCollection === name`
// would retarget a relation the moment a half-typed name happens to equal an
// existing collection ("users" while typing "users_profiles"). The editor
// rebuilds fields with `{ ...field }`, so the flag survives its edits.
interface CreateDraftField extends DraftField {
  selfRelation?: boolean;
}

function selfRelationFlag(previous: CreateDraftField, next: DraftField, collectionName: string): boolean {
  const nextTarget = next.relation?.targetCollection;
  if (nextTarget === undefined) return false;
  if (nextTarget === previous.relation?.targetCollection) return previous.selfRelation === true;
  return nextTarget !== "" && nextTarget === collectionName;
}

export function CollectionCreatePage({ navigate }: { navigate(route: AdminRoute): void }) {
  const { schema } = useBackend();
  const [name, setName] = useState("");
  const [fields, setFields] = useState<CreateDraftField[]>([newDraftField()]);
  const [relationCatalog, setRelationCatalog] = useState<CollectionCatalog | null>(null);
  const [relationCatalogLoading, setRelationCatalogLoading] = useState(false);
  const [relationCatalogFailed, setRelationCatalogFailed] = useState(false);
  const [plan, setPlan] = useState<PlannedChange | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const active = useRouteGuard(adminRouteIdentity({ kind: "collection-create" }));
  const currentInput = useMemo(() => {
    try { return buildInput("create", name, fields); } catch { return null; }
  }, [fields, name]);
  const previewCurrent = plan !== null && currentInput !== null && plan.matches(currentInput);
  const needsRelationCatalog = fields.some((field) => field.type === "relation");
  const relationTargets = useMemo(() => relationTargetOptions(relationCatalog, name), [name, relationCatalog]);

  useEffect(() => {
    if (!needsRelationCatalog || relationCatalog !== null || relationCatalogFailed) return;
    const controller = new AbortController();
    setRelationCatalogLoading(true);
    void schema.catalog(controller.signal).then((catalog) => {
      if (controller.signal.aborted) return;
      setRelationCatalog(catalog);
      setRelationCatalogFailed(false);
    }).catch(() => {
      if (!controller.signal.aborted) setRelationCatalogFailed(true);
    }).finally(() => {
      if (!controller.signal.aborted) setRelationCatalogLoading(false);
    });
    return () => controller.abort();
  }, [needsRelationCatalog, relationCatalog, relationCatalogFailed, schema]);

  function changeField(index: number, field: DraftField) {
    setPlan(null);
    setFields((current) => current.map((currentField, position) => position === index
      ? { ...field, selfRelation: selfRelationFlag(currentField, field, name) }
      : currentField));
  }

  function removeField(index: number) {
    setPlan(null);
    setFields((current) => current.filter((_, position) => position !== index));
  }

  async function preview() {
    setError(null);
    let input: CollectionChange;
    try { input = buildInput("create", name, fields); } catch (caught: unknown) { setError(draftErrorMessage(caught, "Collection input is invalid.")); return; }
    setPending(true);
    try {
      const result = await schema.plan(input);
      if (!active()) return;
      setPlan(result);
    } catch (caught: unknown) {
      if (!active()) return;
      setPlan(null);
      setError(schemaErrorMessage(caught, "Unable to preview the collection."));
    } finally {
      if (active()) setPending(false);
    }
  }

  async function apply() {
    if (!previewCurrent || plan === null) return;
    setPending(true);
    setError(null);
    try {
      await schema.apply(plan);
      if (!active()) return;
      navigate({ kind: "collections" });
    } catch (caught: unknown) {
      if (!active()) return;
      const reason = caught instanceof SchemaError ? caught.reason : "failed";
      if (reason === "same-version-conflict" || reason === "catalog-moved") {
        setPlan(null);
        setError(reason === "same-version-conflict"
          ? "A collection with this name likely already exists."
          : "The catalog changed. Preview the collection again before applying.");
      } else setError(schemaErrorMessage(caught, "Unable to create the collection."));
    } finally {
      if (active()) setPending(false);
    }
  }

  return <section>
    <h2>Create collection</h2>
    <p>Preview the server-generated schema plan before applying it.</p>
    {error && <p role="alert">{error}</p>}
    {needsRelationCatalog && relationCatalogFailed && <p role="alert">Unable to load relation targets. Reload this page before adding a relation field.</p>}
    <form onSubmit={(event) => { event.preventDefault(); void preview(); }}>
      <label htmlFor="collection-name">Collection name</label>
      <input id="collection-name" onChange={(event) => {
        const nextName = event.target.value;
        setPlan(null);
        setFields((current) => current.map((field) => field.selfRelation === true && field.relation !== undefined
          ? { ...field, relation: { ...field.relation, targetCollection: nextName } }
          : field));
        setName(nextName);
      }} required value={name} />
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
          relationTargetsLoading={relationCatalogLoading}
        />)}
        <button className="secondary" onClick={() => { setPlan(null); setFields((current) => [...current, newDraftField()]); }} type="button">Add field</button>
      </fieldset>
      <button disabled={pending} type="submit">{pending ? "Previewing…" : "Preview collection"}</button>
    </form>
    {previewCurrent && plan && <section className="preview" aria-label="Collection preview">
      <h3>Server preview</h3>
      <p>Catalog version {plan.baseVersion}</p>
      <ul>{plan.fields.map((field) => <li key={field.name}>{previewField(field)}</li>)}</ul>
      <h4>SQL</h4>
      <pre>{plan.sql.join("\n\n")}</pre>
      <button disabled={pending} onClick={() => void apply()} type="button">{pending ? "Applying…" : "Apply collection"}</button>
    </section>}
  </section>;
}
