import { useEffect, useMemo, useRef, useState } from "react";
import { APIClientError } from "../api/client";
import { parseJSON, stringifyJSON } from "../api/json";
import {
  collectionFromCatalog,
  collectionFromCatalogByID,
  type CollectionCatalog,
  type CollectionDefinition,
  type FieldDefinition,
} from "../api/collections";
import { CreateAttempt, type RecordsAPI, type RecordData, type RecordValue } from "../api/records";
import { useSession } from "../auth/session";
import { adminRouteIdentity, useRouteGuard, type AdminRoute } from "../routing/admin-location";
import {
  encodeRelationValue,
  relationDraftValue,
  relationErrorText,
  relationTargetOption,
  type RelationTargetOption,
  relationTargetSearchField,
  relationTargetSearchRequest,
  RelationTargetSearchError,
  RelationValueError,
  selectRelationTarget,
} from "./record-relation";

type FieldState =
  | { kind: "unset" }
  | { kind: "null" }
  | { kind: "value"; value: string | boolean };
type Draft = Record<string, FieldState>;

function draftValue(field: FieldDefinition, value: RecordValue | undefined): string | boolean {
  if (field.type === "boolean") return value === true;
  if (field.type === "jsonb") return value === undefined || value === null ? "" : stringifyJSON(value, 2);
  if (field.type === "relation") return relationDraftValue(field, value);
  return value === undefined || value === null ? "" : String(value);
}

function initialState(field: FieldDefinition, value: RecordValue | undefined, isCreate: boolean): FieldState {
  if (isCreate) return { kind: "unset" };
  if (field.type === "relation" && field.relation?.cardinality === "multiple") {
    return { kind: "value", value: draftValue(field, value) };
  }
  if (value === null || value === undefined) return { kind: "null" };
  return { kind: "value", value: draftValue(field, value) };
}

function sameState(left: FieldState | undefined, right: FieldState | undefined): boolean {
  return left?.kind === right?.kind && (left?.kind !== "value" || right?.kind !== "value" || left.value === right.value);
}

function encodeValue(field: FieldDefinition, value: string | boolean): RecordValue {
  if (field.type === "boolean") return value === true;
  const text = String(value);
  if (field.type === "relation") return encodeRelationValue(field, text);
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
  return parseJSON(text) as RecordValue;
}

const fieldErrorText: Record<string, string> = {
  required: "This field is required.",
  type_mismatch: "Value does not match the field type.",
  unknown_field: "This field is not recognized.",
  value_too_long: "Value exceeds the 2,048-byte limit for text values.",
};

// The server bounds indexed and unique text at 2,048 bytes so a btree entry
// always fits, and every other text value at the 1 MiB body limit restated in
// the codec. Naming the wrong bound sends people trimming a value that was
// never near it, so the limit follows the field the same way maxLength does.
function valueTooLongText(field: FieldDefinition): string {
  return field.type === "text" && !field.indexed && !field.unique
    ? "Value exceeds the 1 MiB limit for text values."
    : fieldErrorText.value_too_long;
}

function validationText(field: FieldDefinition, code: string): string {
  if (field.type === "relation") return relationErrorText(field, code);
  if (code === "value_too_long") return valueTooLongText(field);
  return fieldErrorText[code] ?? "Value is invalid.";
}

function RelationTargetSearch({
  disabled,
  draft,
  field,
  onChange,
  records,
  target,
}: {
  disabled: boolean;
  draft: string;
  field: FieldDefinition;
  onChange(value: string): void;
  records: RecordsAPI;
  target: CollectionDefinition;
}) {
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<RelationTargetOption[] | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => () => abortRef.current?.abort(), []);

  async function search() {
	abortRef.current?.abort();
	abortRef.current = null;
	setSearching(false);
    let request: ReturnType<typeof relationTargetSearchRequest>;
    try { request = relationTargetSearchRequest(target, query); }
    catch (caught: unknown) {
      setResults(null);
      setStatus(caught instanceof RelationTargetSearchError && caught.code === "relation_search_invalid_uuid"
        ? "Enter a canonical, non-zero target UUID to search."
        : "The target search value is too long or unsupported.");
      return;
    }
    const controller = new AbortController();
    abortRef.current = controller;
    setSearching(true);
    setStatus(null);
    try {
      const page = await records.list(target.name, { filter: request.filter, sort: request.sort }, controller.signal);
      if (controller.signal.aborted) return;
      const options = page.items.flatMap((record) => {
        const option = relationTargetOption(record, request.displayField);
        return option === undefined ? [] : [option];
      });
      setResults(options);
      setStatus(options.length === 0
        ? "No matching target records."
        : page.nextCursor ? "Showing the first 50 matching target records." : null);
    } catch {
      if (!controller.signal.aborted) {
        setResults(null);
        setStatus("Unable to search target records.");
      }
    } finally {
      if (!controller.signal.aborted) setSearching(false);
    }
  }

  function select(id: string) {
    const selection = selectRelationTarget(field, draft, id);
    if (selection.status === "selected") {
      onChange(selection.draft);
      setStatus(`Selected ${id}.`);
    } else if (selection.status === "duplicate") {
      setStatus("That target is already selected.");
    } else {
      setStatus(`Select no more than ${field.relation?.maxSelect ?? 0} related records.`);
    }
  }

  const searchField = relationTargetSearchField(target);
  const statusID = `record-${field.name}-target-search-status`;
  return <fieldset>
    <legend>{field.name} target lookup</legend>
    <label htmlFor={`record-${field.name}-target-search`}>
      {searchField === undefined ? `${field.name} target UUID` : `${field.name} target ${searchField.name}`}
    </label>
    <input
      aria-describedby={status ? statusID : undefined}
      disabled={disabled || searching}
      id={`record-${field.name}-target-search`}
      maxLength={1024}
      onChange={(event) => setQuery(event.target.value)}
      onKeyDown={(event) => {
        if (event.key === "Enter") { event.preventDefault(); void search(); }
      }}
      placeholder={searchField === undefined ? "Canonical target UUID" : `Exact ${searchField.name}`}
      value={query}
    />
    <button disabled={disabled || searching} onClick={() => void search()} type="button">
      {searching ? "Searching…" : `Search ${field.name} targets`}
    </button>
    {status && <p aria-live="polite" id={statusID}>{status}</p>}
    {results !== null && results.length > 0 && <ul aria-label={`${field.name} target results`}>
      {results.map((option) => <li key={option.id}>
        <button disabled={disabled} onClick={() => select(option.id)} type="button">
          Select {option.label}{searchField === undefined || option.label === option.id ? "" : ` — ${option.id}`}
        </button>
      </li>)}
    </ul>}
  </fieldset>;
}

export function RecordEditPage({ collectionName, id, navigate }: { collectionName: string; id?: string; navigate(route: AdminRoute): void }) {
  const { collections, records } = useSession();
  const [collection, setCollection] = useState<CollectionDefinition | null>(null);
  const [catalog, setCatalog] = useState<CollectionCatalog | null>(null);
  const [draft, setDraft] = useState<Draft>({});
  const [initial, setInitial] = useState<Draft>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [message, setMessage] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [etag, setETag] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const [loadVersion, setLoadVersion] = useState(0);
  const createAttemptRef = useRef(new CreateAttempt());
  const loadedResourceRef = useRef<{ collectionName: string; id?: string } | null>(null);
  const isCreate = id === undefined;
  const active = useRouteGuard(adminRouteIdentity({ kind: "record", collection: collectionName, id }));

  useEffect(() => {
    createAttemptRef.current.reset();
    setErrors({});
  }, [collectionName, id]);

  useEffect(() => {
    const controller = new AbortController();
    const loadedResource = loadedResourceRef.current;
    const preserveStaleDraft = stale && collection !== null && loadedResource?.collectionName === collectionName && loadedResource.id === id;
    if (!preserveStaleDraft) {
      loadedResourceRef.current = null;
      setCollection(null);
      setCatalog(null);
      setStale(false);
      setETag(null);
    }
    setMessage(null);
    void collections.catalog(controller.signal).then(async (catalog) => {
      const definition = collectionFromCatalog(catalog, collectionName);
      if (!definition) throw new Error("missing collection");
      let record: RecordData = {};
      let nextETag: string | null = null;
      if (id) {
        const representation = await records.get(collectionName, id, controller.signal);
        record = representation.data;
        nextETag = representation.etag;
      }
      if (controller.signal.aborted) return;
      const values: Draft = {};
      for (const field of definition.fields) values[field.name] = initialState(field, record[field.name], isCreate);
      loadedResourceRef.current = { collectionName, id };
      setDraft(values);
      setInitial(values);
      setCollection(definition);
      setCatalog(catalog);
      setETag(nextETag);
      setStale(false);
      setErrors({});
    }).catch((caught: unknown) => {
      if (controller.signal.aborted) return;
      if (preserveStaleDraft) setMessage("Unable to reload the latest record. Your draft was preserved; try again.");
      else if (caught instanceof APIClientError && caught.status === 404) setMessage("This record no longer exists.");
      else setMessage("Unable to load the record editor.");
    });
    return () => controller.abort();
  }, [collectionName, collections, id, isCreate, loadVersion, records]);

  const dirty = useMemo(() => new Set(Object.keys(draft).filter((name) => !sameState(draft[name], initial[name]))), [draft, initial]);

  async function save() {
    if (!collection) return;
    const nextErrors: Record<string, string> = {};
    const input: RecordData = {};
    for (const field of collection.fields) {
      if (!isCreate && !dirty.has(field.name)) continue;
      const state = draft[field.name] ?? { kind: "unset" };
      if (state.kind === "unset") {
        if (isCreate && field.type === "relation" && (field.relation?.minSelect ?? 0) > 0) {
          nextErrors[field.name] = validationText(field, "relation_min_select");
        }
        continue;
      }
      if (state.kind === "null") {
        if (field.type === "relation" && field.relation?.cardinality === "multiple") {
          nextErrors[field.name] = validationText(field, "relation_invalid_value");
        } else if (field.type === "relation" && (field.relation?.minSelect ?? 0) > 0) {
          nextErrors[field.name] = validationText(field, "relation_min_select");
        } else {
          input[field.name] = null;
        }
        continue;
      }
      try { input[field.name] = encodeValue(field, state.value); }
      catch (caught: unknown) {
        if (field.type === "relation" && caught instanceof RelationValueError) {
          nextErrors[field.name] = validationText(field, caught.code);
        } else {
          nextErrors[field.name] = field.type === "jsonb" ? "Enter valid JSON." : "Value does not match the field type.";
        }
      }
    }
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    setPending(true);
    setMessage(null);
    try {
      if (isCreate) {
        await records.create(collectionName, input, createAttemptRef.current.keyFor(input));
        createAttemptRef.current.reset();
      } else {
        if (etag === null) throw new Error("record ETag is unavailable");
        await records.update(collectionName, id, input, etag);
      }
      if (!active()) return;
      navigate({ kind: "records", collection: collectionName });
    } catch (caught: unknown) {
      if (!active()) return;
      if (caught instanceof APIClientError && caught.status === 404) setMessage("This record no longer exists.");
      else if (caught instanceof APIClientError && caught.status === 412) {
        setStale(true);
        setMessage("This record changed after you loaded it. Reload the latest version before saving or deleting.");
      } else if (caught instanceof APIClientError && caught.status === 422) {
        const mapped: Record<string, string> = {};
        for (const [name, codes] of Object.entries(caught.details)) {
          const field = collection.fields.find((candidate) => candidate.name === name);
          if (field !== undefined) mapped[name] = validationText(field, codes[0]);
        }
        setErrors(mapped);
        if (Object.keys(mapped).length === 0) setMessage("Record input is invalid.");
      } else setMessage("Unable to save the record.");
    } finally {
      if (active()) setPending(false);
    }
  }

  async function remove() {
    if (!id || etag === null || !window.confirm("Delete this record? This cannot be undone.")) return;
    setPending(true);
    setMessage(null);
    try {
      await records.delete(collectionName, id, etag);
      if (!active()) return;
      navigate({ kind: "records", collection: collectionName });
    }
    catch (caught: unknown) {
      if (!active()) return;
      if (caught instanceof APIClientError && caught.status === 404) setMessage("This record no longer exists.");
      else if (caught instanceof APIClientError && caught.status === 412) {
        setStale(true);
        setMessage("This record changed after you loaded it. Reload the latest version before saving or deleting.");
      } else if (caught instanceof APIClientError && caught.status === 422) {
        // The server reports a restricted relation against the record id, so
        // save()'s field mapping cannot render it.
        setMessage(caught.details["id"]?.includes("relation_delete_restricted")
          ? "Other records still reference this record. Remove those references first."
          : "Unable to delete the record.");
      } else setMessage("Unable to delete the record.");
    }
    finally {
      if (active()) setPending(false);
    }
  }

  return <section>
    <h2>{isCreate ? "Create" : "Edit"} {collectionName} record</h2>
    {message && <p role="alert">{message}</p>}
    {stale && <button onClick={() => setLoadVersion((current) => current + 1)} type="button">Reload record</button>}
    {!collection && !message && <p aria-live="polite">Loading record…</p>}
    {collection && <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      {collection.fields.slice().sort((a, b) => a.ordinal - b.ordinal).map((field) => {
        const state = draft[field.name] ?? { kind: "unset" };
        const relation = field.type === "relation" ? field.relation : undefined;
        const multipleRelation = relation?.cardinality === "multiple";
        const relationHintID = relation === undefined ? undefined : `record-${field.name}-hint`;
        const errorID = errors[field.name] ? `record-${field.name}-error` : undefined;
        const describedBy = [relationHintID, errorID].filter(Boolean).join(" ") || undefined;
        const targetCollection = catalog !== null && relation !== undefined
          ? collectionFromCatalogByID(catalog, relation.targetCollectionId)
          : undefined;
        const targetName = targetCollection?.name;
        return <div className="record-field" key={field.name}>
        <label htmlFor={`record-${field.name}-state`}>{field.name} state</label>
        <select aria-describedby={describedBy} aria-invalid={errors[field.name] ? "true" : undefined} id={`record-${field.name}-state`} onChange={(event) => {
          const kind = event.target.value as FieldState["kind"];
          const next: FieldState = kind === "unset" ? { kind } : kind === "null" ? { kind } : { kind, value: field.type === "boolean" ? false : "" };
          setDraft((current) => ({ ...current, [field.name]: next }));
          setErrors((current) => { const updated = { ...current }; delete updated[field.name]; return updated; });
        }} value={state.kind}>
          {isCreate && <option value="unset">Use default / omit</option>}
          {!multipleRelation && (field.type !== "relation" || (relation?.minSelect ?? 0) === 0 || state.kind === "null") && <option value="null">NULL</option>}
          <option value="value">Value</option>
        </select>
        {relation !== undefined && <p id={relationHintID}>
          Target: {targetName ?? "unknown collection"}. {multipleRelation
            ? `Enter one canonical UUID per line; order is preserved (${relation.minSelect}–${relation.maxSelect} selections).`
            : "Enter one canonical UUID."}
        </p>}
        {state.kind === "value" && <>
        <label htmlFor={`record-${field.name}`}>{field.name}</label>
        {field.type === "boolean" ? <input checked={state.value === true} id={`record-${field.name}`} onChange={(event) => setDraft((current) => ({ ...current, [field.name]: { kind: "value", value: event.target.checked } }))} type="checkbox" /> :
          field.type === "jsonb" || multipleRelation ? <textarea aria-describedby={describedBy} aria-invalid={errors[field.name] ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => setDraft((current) => ({ ...current, [field.name]: { kind: "value", value: event.target.value } }))} rows={multipleRelation ? Math.min(8, Math.max(3, relation?.maxSelect ?? 3)) : 5} value={String(state.value)} /> :
            <input aria-describedby={describedBy} aria-invalid={errors[field.name] ? "true" : undefined} id={`record-${field.name}`} inputMode={["integer", "bigint", "numeric"].includes(field.type) ? "decimal" : undefined} maxLength={field.type === "text" && (field.indexed || field.unique) ? 2048 : undefined} onChange={(event) => setDraft((current) => ({ ...current, [field.name]: { kind: "value", value: event.target.value } }))} value={String(state.value)} />}
        {relation !== undefined && targetCollection !== undefined && <RelationTargetSearch
          disabled={pending || stale}
          draft={String(state.value)}
          field={field}
          key={`${field.name}:${relation.targetCollectionId}`}
          onChange={(value) => setDraft((current) => ({ ...current, [field.name]: { kind: "value", value } }))}
          records={records}
          target={targetCollection}
        />}
        </>}
        {errors[field.name] && <p className="field-error" id={`record-${field.name}-error`}>{errors[field.name]}</p>}
      </div>;})}
      <button disabled={pending || stale} type="submit">{pending ? "Saving…" : isCreate ? "Create record" : "Save changes"}</button>
      {!isCreate && <button className="danger" disabled={pending || stale} onClick={() => void remove()} type="button">Delete record</button>}
    </form>}
  </section>;
}
