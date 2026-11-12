import type {
  Collection,
  CollectionChange,
  CollectionIndex,
  CollectionType,
  FieldInput,
  FieldOptions,
  FieldType,
  IndexInput,
  PlannedCollection,
  RecordIdPolicy,
  RelationInput,
  ValidationMode,
} from "../api/collections";
import { parseJson, stringifyJson } from "../api/http";

// The Schema tab edits a local draft; these pure functions turn the catalog into a
// draft, the draft into a plan request, and a plan back into a readable change list.

export const fieldTypes: FieldType[] = [
  "text", "editor", "integer", "bigint", "numeric", "boolean", "timestamptz", "autodate",
  "jsonb", "relation", "file", "uuid", "geoPoint", "password",
];

/** Option keys the server accepts per type (internal/schema/file_options.go, fieldvalue/options.go). */
export const optionKeys: Record<FieldType, (keyof FieldOptions)[]> = {
  text: ["minLength", "maxLength", "pattern", "format", "allowedDomains", "deniedDomains", "autogeneratePattern", "allowedValues"],
  password: ["minLength", "maxLength", "pattern"],
  integer: ["min", "max"],
  bigint: ["min", "max"],
  numeric: ["min", "max"],
  timestamptz: ["min", "max"],
  jsonb: ["allowedValues", "minSelect", "maxSelect", "maxSize"],
  editor: ["maxSize", "convertURLs"],
  autodate: ["onCreate", "onUpdate"],
  file: ["maxSelect", "maxFileSize", "mimeTypes", "thumbs", "protected"],
  boolean: [],
  uuid: [],
  geoPoint: [],
  relation: [],
};

/** Flags a type can carry; the server rejects the rest. */
export function allowedFlags(type: FieldType): { unique: boolean; indexed: boolean; required: boolean; presentable: boolean; default: boolean } {
  const noIndex = type === "file" || type === "password";
  return {
    unique: !noIndex && type !== "geoPoint" && type !== "autodate",
    indexed: !noIndex,
    required: type !== "autodate",
    presentable: type !== "password",
    default: type !== "file" && type !== "password" && type !== "autodate" && type !== "relation",
  };
}

export interface DraftField extends Omit<FieldInput, "default"> {
  /** Stable row key: the catalog id, or a local id for a new field. */
  key: string;
  /** JSON literal as typed; "" means no default. */
  defaultText: string;
}

export interface DraftIndex extends IndexInput {
  key: string;
}

export interface Draft {
  type: CollectionType;
  name: string;
  compatibilityName: string;
  validationMode: ValidationMode;
  /** Create only; immutable afterwards. */
  recordId: RecordIdPolicy;
  fields: DraftField[];
  indexes: DraftIndex[];
  /** SQL view query (create, or update of a SQL view). */
  viewQuery: string;
}

let sequence = 0;
export const newKey = (prefix: string) => `${prefix}${++sequence}-${Date.now().toString(36)}`;

export function newField(type: FieldType, taken: string[]): DraftField {
  let name = type === "geoPoint" ? "location" : type;
  for (let n = 2; taken.includes(name); n++) name = `${type === "geoPoint" ? "location" : type}${n}`;
  return {
    key: newKey("f"),
    name,
    type,
    required: false,
    unique: false,
    indexed: false,
    hidden: false,
    presentable: false,
    defaultText: "",
    options: type === "autodate" ? { onCreate: true } : undefined,
    relation: type === "relation" ? { targetCollection: "", cardinality: "single", onDelete: "set_null" } : undefined,
  };
}

export function emptyDraft(type: CollectionType, systemFields: FieldInput[] = []): Draft {
  return {
    type,
    name: "",
    compatibilityName: "",
    validationMode: "postgres",
    recordId: { kind: "uuid" },
    fields: systemFields.map((field) => toDraftField(field, newKey("s"))),
    indexes: [],
    viewQuery: "",
  };
}

function toDraftField({ default: value, ...field }: FieldInput, key: string): DraftField {
  return { ...field, key, hidden: field.hidden ?? false, presentable: field.presentable ?? false, defaultText: value === undefined || value === null ? "" : stringifyJson(value) };
}

/** Catalog index -> exactly one input shape (rawSql, keys + where, or fields). */
export function indexInput(index: CollectionIndex): IndexInput {
  if (index.rawSql) return { name: index.name, unique: index.unique, rawSql: index.rawSql };
  if (index.keys?.length) return { name: index.name, unique: index.unique, keys: index.keys, ...(index.where ? { where: index.where } : {}) };
  return { name: index.name, unique: index.unique, fields: index.fields ?? [] };
}

export function draftFromCollection(collection: Collection, catalog: Collection[]): Draft {
  const nameOf = (id: string) => catalog.find((candidate) => candidate.id === id)?.name ?? id;
  return {
    type: collection.type ?? "base",
    name: collection.name,
    compatibilityName: collection.compatibilityName ?? "",
    validationMode: collection.validationMode ?? "postgres",
    recordId: collection.recordId ?? { kind: "uuid" },
    fields: collection.fields.map(({ columnName: _c, ordinal: _o, relation, ...field }) => {
      const input: FieldInput = { ...field };
      if (relation) {
        input.relation = { targetCollection: nameOf(relation.targetCollectionId), cardinality: relation.cardinality, minSelect: relation.minSelect, maxSelect: relation.maxSelect, onDelete: relation.onDelete };
      }
      return toDraftField(input, field.id);
    }),
    indexes: (collection.indexes ?? []).map((index) => ({ ...indexInput(index), key: index.id })),
    viewQuery: collection.view?.sql ?? "",
  };
}

export const sameDraft = (a: Draft, b: Draft) => stringifyJson(a) === stringifyJson(b);

/** Drops unset values and keys the type does not take (left over from a type switch). */
export function pruneOptions(type: FieldType, options: FieldOptions | undefined): FieldOptions | undefined {
  if (!options) return undefined;
  const result: Record<string, unknown> = {};
  for (const key of optionKeys[type]) {
    const value = options[key];
    if (value === undefined || value === "" || (Array.isArray(value) && value.length === 0)) continue;
    if (key !== "protected" && value === false) continue;
    result[key] = value;
  }
  return Object.keys(result).length ? (result as FieldOptions) : undefined;
}

export class DraftInvalid extends Error {
  constructor(readonly details: Record<string, string[]>) {
    super("invalid draft");
  }
}

function fieldInput(field: DraftField, creating: boolean): { input: FieldInput; error?: string } {
  const { key: _key, defaultText, relation, options, help, id, system, ...rest } = field;
  const input: FieldInput = { ...rest };
  if (!creating && id) input.id = id;
  if (system) input.system = true;
  if (help) input.help = help;
  const pruned = pruneOptions(field.type, options);
  if (pruned) input.options = pruned;
  if (field.type === "relation" && relation) {
    const clean: RelationInput = { targetCollection: relation.targetCollection, cardinality: relation.cardinality ?? "single", onDelete: relation.onDelete ?? "set_null" };
    if (relation.minSelect) clean.minSelect = relation.minSelect;
    if (relation.maxSelect) clean.maxSelect = relation.maxSelect;
    input.relation = clean;
  }
  if (defaultText.trim()) {
    try {
      input.default = parseJson(defaultText);
    } catch {
      return { input, error: "invalid_json" };
    }
  }
  return { input };
}

/** The request for one plan, with row keys in the order sent so error paths map back. */
export interface BuiltChange {
  change: CollectionChange;
  fieldKeys: string[];
  indexKeys: string[];
}

export function buildChange(draft: Draft, original: Collection | undefined, base: Draft | undefined): BuiltChange {
  const creating = !original;
  const errors: Record<string, string[]> = {};
  const fieldKeys = draft.fields.map((field) => field.key);
  const indexKeys = draft.indexes.map((index) => index.key);
  const fields = draft.fields.map((field, i) => {
    const { input, error } = fieldInput(field, creating);
    if (error) errors[`fields.${i}.default`] = [error];
    return input;
  });
  if (Object.keys(errors).length) throw new DraftInvalid(errors);
  const indexes = draft.indexes.map(({ key: _key, ...index }) => index);
  const isView = draft.type === "view";
  if (creating) {
    const common = { operation: "create" as const, name: draft.name.trim(), type: draft.type, validationMode: draft.validationMode, ...(draft.compatibilityName ? { compatibilityName: draft.compatibilityName } : {}) };
    if (isView) return { change: { ...common, viewQuery: draft.viewQuery }, fieldKeys: [], indexKeys: [] };
    return { change: { ...common, recordId: draft.recordId, fields, indexes }, fieldKeys, indexKeys };
  }
  const renamed = draft.name.trim() !== original.name;
  // The planner refuses a rename combined with any other change, so a rename is planned alone.
  if (renamed) {
    if (base && !sameDraft({ ...draft, name: base.name }, base)) throw new DraftInvalid({ name: ["rename_alone"] });
    return { change: { operation: "update", name: draft.name.trim(), collectionId: original.id }, fieldKeys: [], indexKeys: [] };
  }
  if (isView) {
    if (!original.view?.sql) throw new DraftInvalid({ name: ["view_read_only"] });
    return { change: { operation: "update", name: original.name, collectionId: original.id, viewQuery: draft.viewQuery }, fieldKeys: [], indexKeys: [] };
  }
  return {
    change: { operation: "update", name: original.name, collectionId: original.id, compatibilityName: draft.compatibilityName, validationMode: draft.validationMode, fields, indexes },
    fieldKeys,
    indexKeys,
  };
}

/** Server validation details ("fields.3.options", "indexes.0.name", "name") mapped onto draft rows. */
export interface MappedErrors {
  fields: Record<string, string[]>;
  indexes: Record<string, string[]>;
  collection: Record<string, string[]>;
  other: string[];
}

export function mapErrors(details: unknown, built: Pick<BuiltChange, "fieldKeys" | "indexKeys">, draft: Draft): MappedErrors {
  const result: MappedErrors = { fields: {}, indexes: {}, collection: {}, other: [] };
  if (!details || typeof details !== "object") return result;
  for (const [path, raw] of Object.entries(details as Record<string, unknown>)) {
    const codes = Array.isArray(raw) ? raw.map(String) : [String(raw)];
    // Import previews prefix paths with the document position (collections.0.fields.2...).
    const local = path.replace(/^(?:auth)?collections\.\d+\./i, "");
    const field = /^fields\.([^.]+)(?:\.(.+))?$/.exec(local);
    const index = /^indexes\.(\d+)(?:\.(.+))?$/.exec(local);
    const add = (bucket: Record<string, string[]>, key: string | undefined, sub: string | undefined) => {
      if (key === undefined) return false;
      (bucket[key] ??= []).push(...codes.map((code) => (sub ? `${sub}: ${code}` : code)));
      return true;
    };
    if (field) {
      const position = Number(field[1]);
      const key = Number.isInteger(position) ? built.fieldKeys[position] : draft.fields.find((candidate) => candidate.name === field[1])?.key;
      if (add(result.fields, key, field[2])) continue;
    }
    if (index && add(result.indexes, built.indexKeys[Number(index[1])], index[2])) continue;
    if (["name", "compatibilityName", "validationMode", "recordId", "viewQuery", "view", "query", "type"].includes(local.split(".")[0])) {
      add(result.collection, local.split(".")[0], undefined);
      continue;
    }
    result.other.push(...codes.map((code) => `${path}: ${code}`));
  }
  return result;
}

type ChangeKind = "add" | "remove" | "rename" | "change" | "move";

export interface ChangeItem {
  kind: ChangeKind;
  target: "collection" | "field" | "index";
  name: string;
  /** Previous name for a rename; changed properties for a change. */
  detail?: string;
  /** Loses data (or may): a dropped field, a type change, a deleted collection. */
  danger: boolean;
}

const comparable = (field: { type: string; required: boolean; unique: boolean; indexed: boolean; hidden?: boolean; presentable?: boolean; help?: string; options?: FieldOptions; default?: unknown }) => ({
  type: field.type,
  required: field.required,
  unique: field.unique,
  indexed: field.indexed,
  hidden: field.hidden ?? false,
  presentable: field.presentable ?? false,
  help: field.help ?? "",
  options: stringifyJson(pruneOptions(field.type as FieldType, field.options) ?? {}),
  default: field.default === undefined || field.default === null ? "" : stringifyJson(field.default),
});

/** What a plan changes relative to the catalog, matched by field id (renames and moves emit no SQL). */
export function diffPlan(current: Collection | undefined, next: PlannedCollection | undefined): ChangeItem[] {
  if (!next) return current ? [{ kind: "remove", target: "collection", name: current.name, danger: true }] : [];
  if (!current) {
    return [
      { kind: "add", target: "collection", name: next.name, danger: false },
      ...next.fields.map((field): ChangeItem => ({ kind: "add", target: "field", name: field.name, detail: field.type, danger: false })),
      ...(next.indexes ?? []).map((index): ChangeItem => ({ kind: "add", target: "index", name: index.name, danger: false })),
    ];
  }
  const items: ChangeItem[] = [];
  if (current.name !== next.name) items.push({ kind: "rename", target: "collection", name: next.name, detail: current.name, danger: false });
  const changedMeta = (["compatibilityName", "validationMode"] as const).filter((key) => (current[key] ?? "") !== (next[key] ?? "") && !(key === "validationMode" && !current[key] && next[key] === "postgres"));
  if (changedMeta.length) items.push({ kind: "change", target: "collection", name: next.name, detail: changedMeta.join(", "), danger: false });
  // A view's columns are derived from its query, so dropping or retyping one loses no stored data.
  const stored = current.type !== "view";
  const byId = new Map(current.fields.map((field) => [field.id, field]));
  const nextIds = new Set(next.fields.map((field) => field.id));
  // Order among the fields both sides share; a pure reorder emits no SQL.
  const oldOrder = current.fields.filter((field) => nextIds.has(field.id)).map((field) => field.id);
  const newOrder = next.fields.filter((field) => field.id && byId.has(field.id)).map((field) => field.id);
  for (const field of next.fields) {
    const previous = field.id ? byId.get(field.id) : undefined;
    if (!previous) {
      items.push({ kind: "add", target: "field", name: field.name, detail: field.type, danger: false });
      continue;
    }
    if (previous.name !== field.name) items.push({ kind: "rename", target: "field", name: field.name, detail: previous.name, danger: false });
    const before = comparable(previous);
    const after = comparable(field);
    const changed: string[] = (Object.keys(before) as (keyof typeof before)[]).filter((key) => before[key] !== after[key]);
    const oldRelation = previous.relation;
    const newRelation = field.relation;
    const cardinalityChanged = Boolean(oldRelation && newRelation && oldRelation.cardinality !== newRelation.cardinality);
    if (oldRelation && newRelation && (cardinalityChanged || oldRelation.onDelete !== newRelation.onDelete || oldRelation.minSelect !== newRelation.minSelect || oldRelation.maxSelect !== newRelation.maxSelect)) changed.push("relation");
    if (changed.length) items.push({ kind: "change", target: "field", name: field.name, detail: changed.join(", "), danger: stored && (changed.includes("type") || cardinalityChanged) });
    if (oldOrder.indexOf(previous.id) !== newOrder.indexOf(previous.id)) items.push({ kind: "move", target: "field", name: field.name, danger: false });
  }
  for (const field of current.fields) {
    if (!nextIds.has(field.id)) items.push({ kind: "remove", target: "field", name: field.name, detail: field.type, danger: stored });
  }
  const oldIndexes = new Map((current.indexes ?? []).map((index) => [index.name, stringifyJson(indexInput(index))]));
  const newIndexes = new Map((next.indexes ?? []).map((index) => [index.name, index]));
  for (const [name, index] of newIndexes) {
    if (!oldIndexes.has(name)) items.push({ kind: "add", target: "index", name, danger: false });
    else if (oldIndexes.get(name) !== stringifyJson(indexInput({ ...index, id: "", indexName: "" }))) items.push({ kind: "change", target: "index", name, danger: false });
  }
  for (const name of oldIndexes.keys()) {
    if (!newIndexes.has(name)) items.push({ kind: "remove", target: "index", name, danger: false });
  }
  return items;
}

/** DDL that destroys data or rewrites a column (a view is only re-created, so DROP VIEW is not). */
export const dangerousStatement = (sql: string) => /\b(DROP\s+(TABLE|COLUMN)|TRUNCATE|DELETE\s+FROM)\b|ALTER\s+COLUMN\s+\S+\s+(SET\s+DATA\s+)?TYPE\b/i.test(sql);

/** Row marker against the draft the editor started from. */
export function rowState<T extends { key: string }>(row: T, base: T[]): "new" | "changed" | undefined {
  const original = base.find((candidate) => candidate.key === row.key);
  if (!original) return "new";
  return stringifyJson(original) === stringifyJson(row) ? undefined : "changed";
}
