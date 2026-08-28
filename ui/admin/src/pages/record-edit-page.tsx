import { useEffect, useMemo, useState } from "react";
import { APIClientError } from "../api/client";
import type { CollectionDefinition, FieldDefinition } from "../api/collections";
import type { RecordData, RecordValue } from "../api/records";
import { useSession } from "../auth/session";

type FieldState =
  | { kind: "unset" }
  | { kind: "null" }
  | { kind: "value"; value: string | boolean };
type Draft = Record<string, FieldState>;

function draftValue(field: FieldDefinition, value: RecordValue | undefined): string | boolean {
  if (field.type === "boolean") return value === true;
  if (field.type === "jsonb") return value === undefined || value === null ? "" : JSON.stringify(value, null, 2);
  return value === undefined || value === null ? "" : String(value);
}

function initialState(field: FieldDefinition, value: RecordValue | undefined, isCreate: boolean): FieldState {
  if (isCreate) return { kind: "unset" };
  if (value === null || value === undefined) return { kind: "null" };
  return { kind: "value", value: draftValue(field, value) };
}

function sameState(left: FieldState | undefined, right: FieldState | undefined): boolean {
  return left?.kind === right?.kind && (left?.kind !== "value" || right?.kind !== "value" || left.value === right.value);
}

function encodeValue(field: FieldDefinition, value: string | boolean): RecordValue {
  if (field.type === "boolean") return value === true;
  const text = String(value);
  if (field.type === "integer") {
    if (!/^-?\d+$/.test(text)) throw new Error("type");
    const integer = BigInt(text);
    if (integer < -2147483648n || integer > 2147483647n) throw new Error("type");
    return Number(integer);
  }
  if (field.type === "bigint" || field.type === "numeric" || field.type === "text") return text;
  if (field.type === "timestamptz") {
    const parsed = new Date(text);
    if (Number.isNaN(parsed.valueOf())) throw new Error("type");
    return parsed.toISOString();
  }
  if (field.type === "uuid") {
    if (!/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(text)) throw new Error("type");
    return text.toLowerCase();
  }
  return JSON.parse(text) as RecordValue;
}

const fieldErrorText: Record<string, string> = {
  required: "This field is required.",
  type_mismatch: "Value does not match the field type.",
  unknown_field: "This field is not recognized.",
};

export function RecordEditPage({ collectionName, id, navigate }: { collectionName: string; id?: string; navigate(path: string): void }) {
  const { collections, records } = useSession();
  const [collection, setCollection] = useState<CollectionDefinition | null>(null);
  const [draft, setDraft] = useState<Draft>({});
  const [initial, setInitial] = useState<Draft>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [message, setMessage] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const isCreate = id === undefined;

  useEffect(() => {
    const controller = new AbortController();
    void collections.catalog(controller.signal).then(async (catalog) => {
      const definition = catalog.collections[collectionName];
      if (!definition) throw new Error("missing collection");
      setCollection(definition);
      let record: RecordData = {};
      if (id) record = await records.get(collectionName, id, controller.signal);
      const values: Draft = {};
      for (const field of definition.fields) values[field.name] = initialState(field, record[field.name], isCreate);
      setDraft(values);
      setInitial(values);
    }).catch((caught: unknown) => {
      if (controller.signal.aborted) return;
      if (caught instanceof APIClientError && caught.status === 404) setMessage("This record no longer exists.");
      else setMessage("Unable to load the record editor.");
    });
    return () => controller.abort();
  }, [collectionName, collections, id, isCreate, records]);

  const dirty = useMemo(() => new Set(Object.keys(draft).filter((name) => !sameState(draft[name], initial[name]))), [draft, initial]);

  async function save() {
    if (!collection) return;
    const nextErrors: Record<string, string> = {};
    const input: RecordData = {};
    for (const field of collection.fields) {
      if (!isCreate && !dirty.has(field.name)) continue;
      const state = draft[field.name] ?? { kind: "unset" };
      if (state.kind === "unset") continue;
      if (state.kind === "null") { input[field.name] = null; continue; }
      try { input[field.name] = encodeValue(field, state.value); }
      catch { nextErrors[field.name] = field.type === "jsonb" ? "Enter valid JSON." : "Value does not match the field type."; }
    }
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    setPending(true);
    setMessage(null);
    try {
      if (isCreate) await records.create(collectionName, input);
      else await records.update(collectionName, id, input);
      navigate(`/admin/collections/${encodeURIComponent(collectionName)}/records`);
    } catch (caught: unknown) {
      if (caught instanceof APIClientError && caught.status === 404) setMessage("This record no longer exists.");
      else if (caught instanceof APIClientError && caught.status === 422) {
        const mapped: Record<string, string> = {};
        for (const [name, codes] of Object.entries(caught.details)) if (collection.fields.some((field) => field.name === name)) mapped[name] = fieldErrorText[codes[0]] ?? "Value is invalid.";
        setErrors(mapped);
        if (Object.keys(mapped).length === 0) setMessage("Record input is invalid.");
      } else setMessage("Unable to save the record.");
    } finally { setPending(false); }
  }

  async function remove() {
    if (!id || !window.confirm("Delete this record? This cannot be undone.")) return;
    setPending(true);
    try { await records.delete(collectionName, id); navigate(`/admin/collections/${encodeURIComponent(collectionName)}/records`); }
    catch (caught: unknown) { setMessage(caught instanceof APIClientError && caught.status === 404 ? "This record no longer exists." : "Unable to delete the record."); }
    finally { setPending(false); }
  }

  return <section>
    <h2>{isCreate ? "Create" : "Edit"} {collectionName} record</h2>
    {message && <p role="alert">{message}</p>}
    {!collection && !message && <p aria-live="polite">Loading record…</p>}
    {collection && <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      {collection.fields.slice().sort((a, b) => a.ordinal - b.ordinal).map((field) => {
        const state = draft[field.name] ?? { kind: "unset" };
        return <div className="record-field" key={field.name}>
        <label htmlFor={`record-${field.name}-state`}>{field.name} state</label>
        <select aria-describedby={errors[field.name] ? `record-${field.name}-error` : undefined} aria-invalid={errors[field.name] ? "true" : undefined} id={`record-${field.name}-state`} onChange={(event) => {
          const kind = event.target.value as FieldState["kind"];
          const next: FieldState = kind === "unset" ? { kind } : kind === "null" ? { kind } : { kind, value: field.type === "boolean" ? false : "" };
          setDraft((current) => ({ ...current, [field.name]: next }));
          setErrors((current) => { const updated = { ...current }; delete updated[field.name]; return updated; });
        }} value={state.kind}>
          {isCreate && <option value="unset">Use default / omit</option>}
          <option value="null">NULL</option>
          <option value="value">Value</option>
        </select>
        {state.kind === "value" && <>
        <label htmlFor={`record-${field.name}`}>{field.name}</label>
        {field.type === "boolean" ? <input checked={state.value === true} id={`record-${field.name}`} onChange={(event) => setDraft((current) => ({ ...current, [field.name]: { kind: "value", value: event.target.checked } }))} type="checkbox" /> :
          field.type === "jsonb" ? <textarea aria-describedby={errors[field.name] ? `record-${field.name}-error` : undefined} aria-invalid={errors[field.name] ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => setDraft((current) => ({ ...current, [field.name]: { kind: "value", value: event.target.value } }))} rows={5} value={String(state.value)} /> :
            <input aria-describedby={errors[field.name] ? `record-${field.name}-error` : undefined} aria-invalid={errors[field.name] ? "true" : undefined} id={`record-${field.name}`} inputMode={["integer", "bigint", "numeric"].includes(field.type) ? "decimal" : undefined} onChange={(event) => setDraft((current) => ({ ...current, [field.name]: { kind: "value", value: event.target.value } }))} value={String(state.value)} />}
        </>}
        {errors[field.name] && <p className="field-error" id={`record-${field.name}-error`}>{errors[field.name]}</p>}
      </div>;})}
      <button disabled={pending} type="submit">{pending ? "Saving…" : isCreate ? "Create record" : "Save changes"}</button>
      {!isCreate && <button className="danger" disabled={pending} onClick={() => void remove()} type="button">Delete record</button>}
    </form>}
  </section>;
}
