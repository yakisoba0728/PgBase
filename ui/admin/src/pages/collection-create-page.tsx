import { useMemo, useState } from "react";
import { APIClientError } from "../api/client";
import { parseJSON, stringifyJSON } from "../api/json";
import { fieldTypes, type CollectionChangeInput, type FieldInput, type FieldType, type CollectionPlan } from "../api/collections";
import { useSession } from "../auth/session";

export interface DraftField extends Omit<FieldInput, "default"> { hasDefault: boolean; defaultText: string }
const newField = (): DraftField => ({ name: "", type: "text", required: false, unique: false, indexed: false, hasDefault: false, defaultText: "" });

export function parseDefault(type: FieldType, value: string): unknown {
  if (type === "boolean") {
    if (value !== "true" && value !== "false") throw new TypeError("boolean");
    return value === "true";
  }
  if (type === "integer" || type === "bigint") {
    if (!/^-?[0-9]+$/.test(value)) throw new TypeError("integer");
    return value;
  }
  if (type === "numeric") {
    if (!/^-?[0-9]+(?:\.[0-9]+)?$/.test(value)) throw new TypeError("number");
    return value;
  }
  if (type === "timestamptz" && value === "now") return { kind: "now" };
  if (type === "jsonb") return parseJSON(value);
  return value;
}

export function buildInput(operation: "create" | "update", name: string, fields: DraftField[]): CollectionChangeInput {
  return {
    operation,
    name,
    fields: fields.map(({ defaultText, hasDefault, ...field }) => hasDefault ? { ...field, default: parseDefault(field.type, defaultText) } : field),
  };
}

function previewField(field: CollectionPlan["definition"]["fields"][number]): string {
  const flags = [field.required && "required", field.unique && "unique", field.indexed && "indexed", field.default !== undefined && `default: ${typeof field.default === "string" ? field.default : stringifyJSON(field.default)}`].filter(Boolean);
  return `${field.name} · ${field.type}${flags.length > 0 ? ` · ${flags.join(" · ")}` : ""}`;
}

export function CollectionCreatePage({ navigate }: { navigate(path: string): void }) {
  const { collections } = useSession();
  const [name, setName] = useState("");
  const [fields, setFields] = useState<DraftField[]>([newField()]);
  const [plan, setPlan] = useState<CollectionPlan | null>(null);
  const [plannedInput, setPlannedInput] = useState<CollectionChangeInput | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputSignature = useMemo(() => {
    try { return stringifyJSON(buildInput("create", name, fields)); } catch { return "invalid"; }
  }, [fields, name]);
  const previewCurrent = plan !== null && plannedInput !== null && stringifyJSON(plannedInput) === inputSignature;

  function changeField(index: number, patch: Partial<DraftField>) {
    setPlan(null);
    setPlannedInput(null);
    setFields((current) => current.map((field, position) => position === index ? { ...field, ...patch } : field));
  }

  function removeField(index: number) {
    setPlan(null);
    setPlannedInput(null);
    setFields((current) => current.filter((_, position) => position !== index));
  }

  async function preview() {
    setError(null);
    let input: CollectionChangeInput;
    try { input = buildInput("create", name, fields); } catch { setError("Enter a valid default for the selected field type."); return; }
    setPending(true);
    try {
      const result = await collections.plan(input);
      setPlannedInput(input);
      setPlan(result);
    } catch (caught: unknown) {
      setPlan(null);
      setPlannedInput(null);
      setError(caught instanceof APIClientError && caught.code === "validation_failed" ? "Collection input is invalid." : "Unable to preview the collection.");
    } finally { setPending(false); }
  }

  async function apply() {
    if (!previewCurrent || plan === null || plannedInput === null) return;
    setPending(true);
    setError(null);
    try {
      await collections.apply(plannedInput, plan.checksum, plan.baseVersion);
      navigate("/admin/collections");
    } catch (caught: unknown) {
      if (caught instanceof APIClientError && caught.status === 409) {
        setPlan(null);
        setPlannedInput(null);
        setError("The catalog changed. Preview the collection again before applying.");
      } else setError("Unable to create the collection.");
    } finally { setPending(false); }
  }

  return <section>
    <h2>Create collection</h2>
    <p>Preview the server-generated schema plan before applying it.</p>
    {error && <p role="alert">{error}</p>}
    <form onSubmit={(event) => { event.preventDefault(); void preview(); }}>
      <label htmlFor="collection-name">Collection name</label>
      <input id="collection-name" onChange={(event) => { setPlan(null); setPlannedInput(null); setName(event.target.value); }} required value={name} />
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
          {fields.length > 1 && <button className="secondary" onClick={() => removeField(index)} type="button">Remove field</button>}
        </div>)}
        <button className="secondary" onClick={() => { setPlan(null); setPlannedInput(null); setFields((current) => [...current, newField()]); }} type="button">Add field</button>
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
