import { useEffect, useMemo, useState } from "react";
import { APIClientError } from "../api/client";
import { stringifyJSON } from "../api/json";
import { fieldTypes, type CollectionChangeInput, type CollectionDefinition, type CollectionPlan, type FieldType } from "../api/collections";
import { useSession } from "../auth/session";
import { buildInput, type DraftField } from "./collection-create-page";

function defaultTextFor(type: string, value: unknown): string {
  if (value === undefined) return "";
  if (type === "timestamptz" && value !== null && typeof value === "object" && (value as { kind?: string }).kind === "now") {
    // The catalog stores the canonical {"kind":"now"} object; the form edits
    // the shorthand text that parseDefault accepts.
    return "now";
  }
  return typeof value === "string" ? value : stringifyJSON(value);
}

function draftFromDefinition(definition: CollectionDefinition): DraftField[] {
  return definition.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .map((field) => ({
      name: field.name,
      type: field.type,
      required: field.required,
      unique: field.unique,
      indexed: field.indexed,
      hasDefault: field.default !== undefined,
      defaultText: defaultTextFor(field.type, field.default),
    }));
}

function toInput(definition: CollectionDefinition, fields: DraftField[]): CollectionChangeInput {
  return buildInput("update", definition.name, fields);
}

function previewField(field: { name: string; type: FieldType; required: boolean; unique: boolean; indexed: boolean; default?: unknown }): string {
  const flags = [field.required && "required", field.unique && "unique", field.indexed && "indexed", field.default !== undefined && `default: ${typeof field.default === "string" ? field.default : stringifyJSON(field.default)}`].filter(Boolean);
  return `${field.name} · ${field.type}${flags.length > 0 ? ` · ${flags.join(" · ")}` : ""}`;
}

export function CollectionEditPage({ collectionName, navigate }: { collectionName: string; navigate(path: string): void }) {
  const { collections } = useSession();
  const [definition, setDefinition] = useState<CollectionDefinition | null>(null);
  const [fields, setFields] = useState<DraftField[]>([]);
  const [plan, setPlan] = useState<CollectionPlan | null>(null);
  const [plannedInput, setPlannedInput] = useState<CollectionChangeInput | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notFound, setNotFound] = useState(false);

  // Delete flow state: null → no delete in progress; otherwise the SQL preview is shown.
  const [deletePlan, setDeletePlan] = useState<CollectionPlan | null>(null);
  const [deletePending, setDeletePending] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void collections.catalog(controller.signal).then((catalog) => {
      const existing = catalog.collections[collectionName];
      if (!existing) { setNotFound(true); return; }
      setDefinition(existing);
      setFields(draftFromDefinition(existing));
    }).catch(() => { if (!controller.signal.aborted) setNotFound(true); });
    return () => controller.abort();
  }, [collectionName, collections]);

  const inputSignature = useMemo(() => {
    if (definition === null) return "";
    try { return stringifyJSON(toInput(definition, fields)); } catch { return "invalid"; }
  }, [definition, fields]);
  const previewCurrent = plan !== null && plannedInput !== null && stringifyJSON(plannedInput) === inputSignature;

  function resetPlan() {
    setPlan(null);
    setPlannedInput(null);
  }

  function changeField(index: number, patch: Partial<DraftField>) {
    resetPlan();
    setFields((current) => current.map((field, position) => position === index ? { ...field, ...patch } : field));
  }

  function addField() {
    resetPlan();
    setFields((current) => [...current, { name: "", type: "text", required: false, unique: false, indexed: false, hasDefault: false, defaultText: "" }]);
  }

  function removeField(index: number) {
    resetPlan();
    setFields((current) => current.filter((_, position) => position !== index));
  }

  async function preview() {
    if (definition === null || pending) return;
    setError(null);
    let input: CollectionChangeInput;
    try { input = toInput(definition, fields); } catch { setError("Enter a valid default for the selected field type."); return; }
    setPending(true);
    try {
      const result = await collections.plan(input);
      setPlannedInput(input);
      setPlan(result);
    } catch (caught: unknown) {
      resetPlan();
      setError(caught instanceof APIClientError && caught.code === "validation_failed" ? "Collection input is invalid." : "Unable to preview the collection change.");
    } finally { setPending(false); }
  }

  async function apply() {
    if (!previewCurrent || plan === null || plannedInput === null || pending) return;
    setPending(true);
    setError(null);
    try {
      await collections.apply(plannedInput, plan.checksum, plan.baseVersion);
      navigate(`/admin/collections/${encodeURIComponent(definition?.name ?? collectionName)}/records`);
    } catch (caught: unknown) {
      if (caught instanceof APIClientError && caught.status === 409) {
        resetPlan();
        setError("The catalog changed. Preview the collection again before applying.");
      } else setError("Unable to apply the collection change.");
    } finally { setPending(false); }
  }

  async function previewDelete() {
    if (definition === null || deletePending) return;
    setDeleteError(null);
    setDeletePending(true);
    try {
      setDeletePlan(await collections.plan({ operation: "delete", name: definition.name }));
    } catch {
      setDeleteError("Unable to preview the collection deletion.");
    } finally { setDeletePending(false); }
  }

  async function applyDelete() {
    if (definition === null || deletePlan === null || deletePending) return;
    setDeletePending(true);
    setDeleteError(null);
    try {
      await collections.apply({ operation: "delete", name: definition.name }, deletePlan.checksum, deletePlan.baseVersion);
      navigate("/admin/collections");
    } catch (caught: unknown) {
      setDeletePlan(null);
      if (caught instanceof APIClientError && caught.status === 409) {
        setDeleteError("The catalog changed. Preview the deletion again before applying.");
      } else setDeleteError("Unable to delete the collection.");
    } finally { setDeletePending(false); }
  }

  if (notFound) return <section><h2>Edit collection</h2><p role="alert">Collection not found.</p></section>;
  if (definition === null) return <section><h2>Edit collection</h2><p aria-live="polite">Loading collection…</p></section>;

  return <section>
    <h2>Edit collection</h2>
    <p>{definition.name} · physical table {definition.tableName}</p>
    {error && <p role="alert">{error}</p>}
    <form onSubmit={(event) => { event.preventDefault(); void preview(); }}>
      <fieldset>
        <legend>Fields</legend>
        {fields.map((field, index) => <div className="field-card" key={index}>
          <label htmlFor={`field-${index}-name`}>Field {index + 1} name</label>
          <input id={`field-${index}-name`} onChange={(event) => changeField(index, { name: event.target.value })} required value={field.name} />
          <label htmlFor={`field-${index}-type`}>Field {index + 1} type</label>
          <select id={`field-${index}-type`} onChange={(event) => changeField(index, { type: event.target.value as FieldType })} value={field.type}>{fieldTypes.map((type) => <option key={type}>{type}</option>)}</select>
          <label><input aria-label={`Field ${index + 1} has default`} checked={field.hasDefault} onChange={(event) => changeField(index, { hasDefault: event.target.checked })} type="checkbox" /> Use default</label>
          <label htmlFor={`field-${index}-default`}>Field {index + 1} default</label>
          {field.type === "boolean" ? <select disabled={!field.hasDefault} id={`field-${index}-default`} onChange={(event) => changeField(index, { defaultText: event.target.value })} value={field.defaultText}><option value="">Choose…</option><option value="true">true</option><option value="false">false</option></select> :
            <input disabled={!field.hasDefault} id={`field-${index}-default`} onChange={(event) => changeField(index, { defaultText: event.target.value })} placeholder={field.type === "timestamptz" ? "now or RFC 3339 timestamp" : field.type === "jsonb" ? "Valid JSON" : undefined} value={field.defaultText} />}
          <div className="checks">
            {(["required", "unique", "indexed"] as const).map((flag) => <label key={flag}><input aria-label={`Field ${index + 1} ${flag}`} checked={field[flag]} onChange={(event) => changeField(index, { [flag]: event.target.checked })} type="checkbox" /> {flag}</label>)}
          </div>
          {fields.length > 1 && <button className="secondary" disabled={pending} onClick={() => removeField(index)} type="button">Remove field</button>}
        </div>)}
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
      <button disabled={pending} onClick={() => void apply()} type="button">{pending ? "Applying…" : "Apply change"}</button>
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
