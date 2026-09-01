import { useEffect, useMemo, useState } from "react";
import { APIClientError } from "../api/client";
import { stringifyJSON } from "../api/json";
import { fieldTypes, type CollectionCatalog, type CollectionChangeInput, type CollectionPlan } from "../api/collections";
import { useSession } from "../auth/session";
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

export { buildInput, parseDefault } from "./collection-field-editor";
export type { DraftField } from "./collection-field-editor";

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

export function CollectionCreatePage({ navigate }: { navigate(path: string): void }) {
  const { collections } = useSession();
  const [name, setName] = useState("");
  const [fields, setFields] = useState<CreateDraftField[]>([newDraftField()]);
  const [relationCatalog, setRelationCatalog] = useState<CollectionCatalog | null>(null);
  const [relationCatalogLoading, setRelationCatalogLoading] = useState(false);
  const [relationCatalogFailed, setRelationCatalogFailed] = useState(false);
  const [plan, setPlan] = useState<CollectionPlan | null>(null);
  const [plannedInput, setPlannedInput] = useState<CollectionChangeInput | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputSignature = useMemo(() => {
    try { return stringifyJSON(buildInput("create", name, fields)); } catch { return "invalid"; }
  }, [fields, name]);
  const previewCurrent = plan !== null && plannedInput !== null && stringifyJSON(plannedInput) === inputSignature;
  const needsRelationCatalog = fields.some((field) => field.type === "relation");
  const relationTargets = useMemo(() => relationTargetOptions(relationCatalog, name), [name, relationCatalog]);

  useEffect(() => {
    if (!needsRelationCatalog || relationCatalog !== null || relationCatalogFailed) return;
    const controller = new AbortController();
    setRelationCatalogLoading(true);
    void collections.catalog(controller.signal).then((catalog) => {
      setRelationCatalog(catalog);
      setRelationCatalogFailed(false);
    }).catch(() => {
      if (!controller.signal.aborted) setRelationCatalogFailed(true);
    }).finally(() => {
      if (!controller.signal.aborted) setRelationCatalogLoading(false);
    });
    return () => controller.abort();
  }, [collections, needsRelationCatalog, relationCatalog, relationCatalogFailed]);

  function changeField(index: number, field: DraftField) {
    setPlan(null);
    setPlannedInput(null);
    setFields((current) => current.map((currentField, position) => position === index
      ? { ...field, selfRelation: selfRelationFlag(currentField, field, name) }
      : currentField));
  }

  function removeField(index: number) {
    setPlan(null);
    setPlannedInput(null);
    setFields((current) => current.filter((_, position) => position !== index));
  }

  async function preview() {
    setError(null);
    let input: CollectionChangeInput;
    try { input = buildInput("create", name, fields); } catch (caught: unknown) { setError(draftErrorMessage(caught, "Collection input is invalid.")); return; }
    setPending(true);
    try {
      const result = await collections.plan(input);
      setPlannedInput(input);
      setPlan(result);
    } catch (caught: unknown) {
      setPlan(null);
      setPlannedInput(null);
      setError(schemaErrorMessage(caught, "Unable to preview the collection."));
    } finally { setPending(false); }
  }

  async function apply() {
    if (!previewCurrent || plan === null || plannedInput === null) return;
    const appliedBaseVersion = plan.baseVersion;
    setPending(true);
    setError(null);
    try {
      await collections.apply(plannedInput, plan.checksum, plan.baseVersion);
      navigate("/admin/collections");
    } catch (caught: unknown) {
      if (caught instanceof APIClientError && caught.status === 409) {
        setPlan(null);
        setPlannedInput(null);
        // A same-version 409 usually means the name is already taken; a moved
        // catalog means the preview was stale.
        try {
          const catalog = await collections.catalog();
          if (catalog.version === appliedBaseVersion) {
            setError("A collection with this name likely already exists.");
          } else {
            setError("The catalog changed. Preview the collection again before applying.");
          }
        } catch {
          setError("The catalog changed. Preview the collection again before applying.");
        }
      } else setError(schemaErrorMessage(caught, "Unable to create the collection."));
    } finally { setPending(false); }
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
        setPlannedInput(null);
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
        <button className="secondary" onClick={() => { setPlan(null); setPlannedInput(null); setFields((current) => [...current, newDraftField()]); }} type="button">Add field</button>
      </fieldset>
      <button disabled={pending} type="submit">{pending ? "Previewing…" : "Preview collection"}</button>
    </form>
    {previewCurrent && plan && <section className="preview" aria-label="Collection preview">
      <h3>Server preview</h3>
      <p>Catalog version {plan.baseVersion}</p>
      <ul>{plan.definition.fields.map((field) => <li key={field.name}>{previewField(field)}</li>)}</ul>
      <h4>SQL</h4>
      <pre>{plan.sql.join("\n\n")}</pre>
      <button disabled={pending} onClick={() => void apply()} type="button">{pending ? "Applying…" : "Apply collection"}</button>
    </section>}
  </section>;
}
