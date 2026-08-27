import { useMemo, useState } from "react";
import { APIClientError } from "../api/client";
import { fieldTypes, type CreateCollectionInput, type FieldInput, type FieldType, type CollectionPlan } from "../api/collections";
import { useSession } from "../auth/session";

interface DraftField extends Omit<FieldInput, "default"> { defaultText: string }
const newField = (): DraftField => ({ name: "", type: "text", required: false, unique: false, indexed: false, defaultText: "" });

function parseDefault(type: FieldType, value: string): unknown {
  if (type === "boolean") return value === "true";
  if (type === "integer") return Number(value);
  if (type === "jsonb") return JSON.parse(value) as unknown;
  return value;
}

function buildInput(name: string, fields: DraftField[]): CreateCollectionInput {
  return { name, fields: fields.map(({ defaultText, ...field }) => defaultText === "" ? field : { ...field, default: parseDefault(field.type, defaultText) }) };
}

function previewField(field: CollectionPlan["definition"]["fields"][number]): string {
  const flags = [field.required && "required", field.unique && "unique", field.indexed && "indexed", field.default !== undefined && `default: ${typeof field.default === "string" ? field.default : JSON.stringify(field.default)}`].filter(Boolean);
  return `${field.name} · ${field.type}${flags.length > 0 ? ` · ${flags.join(" · ")}` : ""}`;
}

export function CollectionCreatePage({ navigate }: { navigate(path: string): void }) {
  const { collections } = useSession();
  const [name, setName] = useState("");
  const [fields, setFields] = useState<DraftField[]>([newField()]);
  const [plan, setPlan] = useState<CollectionPlan | null>(null);
  const [plannedInput, setPlannedInput] = useState<CreateCollectionInput | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const inputSignature = useMemo(() => {
    try { return JSON.stringify(buildInput(name, fields)); } catch { return "invalid"; }
  }, [fields, name]);
  const previewCurrent = plan !== null && plannedInput !== null && JSON.stringify(plannedInput) === inputSignature;

  function changeField(index: number, patch: Partial<DraftField>) {
    setPlan(null);
    setPlannedInput(null);
    setFields((current) => current.map((field, position) => position === index ? { ...field, ...patch } : field));
  }

  async function preview() {
    setError(null);
    let input: CreateCollectionInput;
    try { input = buildInput(name, fields); } catch { setError("Enter a valid JSON value for JSONB defaults."); return; }
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
          <label htmlFor={`field-${index}-default`}>Field {index + 1} default</label>
          <input id={`field-${index}-default`} onChange={(event) => changeField(index, { defaultText: event.target.value })} value={field.defaultText} />
          <div className="checks">
            {(["required", "unique", "indexed"] as const).map((flag) => <label key={flag}><input aria-label={`Field ${index + 1} ${flag}`} checked={field[flag]} onChange={(event) => changeField(index, { [flag]: event.target.checked })} type="checkbox" /> {flag}</label>)}
          </div>
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
