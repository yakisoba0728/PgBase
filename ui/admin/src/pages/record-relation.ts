import type { CollectionDefinition, FieldDefinition } from "../api/collections";
import type { RecordData, RecordValue } from "../api/records";

const canonicalUUIDPattern = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i;
const nilUUID = "00000000-0000-0000-0000-000000000000";
const filterFieldPattern = /^[a-z_][a-z0-9_]*$/;
const maximumRelationSearchBytes = 1024;

export type RelationValueErrorCode =
  | "relation_invalid_value"
  | "duplicate_relation_value"
  | "relation_min_select"
  | "relation_max_select";

export class RelationValueError extends Error {
  constructor(public readonly code: RelationValueErrorCode) {
    super(code);
    this.name = "RelationValueError";
  }
}

function relationDefinition(field: FieldDefinition) {
  if (field.type !== "relation" || field.relation === undefined) {
    throw new RelationValueError("relation_invalid_value");
  }
  return field.relation;
}

function canonicalUUID(value: string): string {
  const normalized = value.trim().toLowerCase();
  if (!canonicalUUIDPattern.test(normalized) || normalized === nilUUID) {
    throw new RelationValueError("relation_invalid_value");
  }
  return normalized;
}

export type RelationTargetSearchErrorCode = "relation_search_invalid_uuid" | "relation_search_too_long";

export class RelationTargetSearchError extends Error {
  constructor(public readonly code: RelationTargetSearchErrorCode) {
    super(code);
    this.name = "RelationTargetSearchError";
  }
}

export interface RelationTargetSearchRequest {
  displayField?: FieldDefinition;
  filter: string;
  sort?: string;
}

export interface RelationTargetOption {
  id: string;
  label: string;
}

export type RelationSelectionResult = {
  draft: string;
  status: "selected" | "duplicate" | "max";
};

export function relationTargetSearchField(collection: CollectionDefinition): FieldDefinition | undefined {
  return collection.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .find((field) => field.type === "text" && (field.indexed || field.unique) && filterFieldPattern.test(field.name));
}

function quotedFilterLiteral(value: string): string {
  return `'${value.replaceAll("'", "''")}'`;
}

/** Builds one exact, indexed lookup. RecordsAPI supplies the fixed limit=50 bound. */
export function relationTargetSearchRequest(collection: CollectionDefinition, query: string): RelationTargetSearchRequest {
  const displayField = relationTargetSearchField(collection);
  if (displayField === undefined) {
    let id: string;
    try { id = canonicalUUID(query); }
    catch { throw new RelationTargetSearchError("relation_search_invalid_uuid"); }
    return { filter: `id = ${quotedFilterLiteral(id)}` };
  }
  if (query.includes("\0") || new TextEncoder().encode(query).length > maximumRelationSearchBytes) {
    throw new RelationTargetSearchError("relation_search_too_long");
  }
  return {
    displayField,
    filter: `${displayField.name} = ${quotedFilterLiteral(query)}`,
    sort: displayField.name,
  };
}

/** Ignores malformed server rows rather than introducing invalid IDs into a relation draft. */
export function relationTargetOption(record: RecordData, displayField?: FieldDefinition): RelationTargetOption | undefined {
  if (typeof record.id !== "string") return undefined;
  let id: string;
  try { id = canonicalUUID(record.id); }
  catch { return undefined; }
  const display = displayField === undefined ? undefined : record[displayField.name];
  return { id, label: typeof display === "string" && display !== "" ? display : id };
}

/** Applies a picker selection while preserving ordered-unique multi relation semantics. */
export function selectRelationTarget(field: FieldDefinition, draft: string, selectedID: string): RelationSelectionResult {
  const relation = relationDefinition(field);
  const id = canonicalUUID(selectedID);
  if (relation.cardinality === "single") {
    return { draft: id, status: "selected" };
  }

  const members = draft.split(/\r?\n/).map((member) => member.trim()).filter((member) => member !== "");
  const duplicate = members.some((member) => {
    try { return canonicalUUID(member) === id; } catch { return false; }
  });
  if (duplicate) return { draft: members.join("\n"), status: "duplicate" };
  if (members.length >= relation.maxSelect) return { draft: members.join("\n"), status: "max" };
  return { draft: [...members, id].join("\n"), status: "selected" };
}

export function relationDraftValue(field: FieldDefinition, value: RecordValue | undefined): string {
  const relation = relationDefinition(field);
  if (value === undefined || value === null) return "";
  if (relation.cardinality === "single") {
    if (typeof value !== "string") return String(value);
    try { return canonicalUUID(value); } catch { return value; }
  }
  if (!Array.isArray(value)) return String(value);
  return value.map((member) => {
    if (typeof member !== "string") return String(member);
    try { return canonicalUUID(member); } catch { return member; }
  }).join("\n");
}

export function encodeRelationValue(field: FieldDefinition, draft: string): RecordValue {
  const relation = relationDefinition(field);
  if (relation.cardinality === "single") return canonicalUUID(draft);

  const members = draft
    .split(/\r?\n/)
    .map((member) => member.trim())
    .filter((member) => member !== "");
  if (members.length < relation.minSelect) throw new RelationValueError("relation_min_select");
  if (members.length > relation.maxSelect) throw new RelationValueError("relation_max_select");

  const canonical = members.map(canonicalUUID);
  if (new Set(canonical).size !== canonical.length) {
    throw new RelationValueError("duplicate_relation_value");
  }
  return canonical;
}

function selectionLabel(count: number): string {
  return `${count} related record${count === 1 ? "" : "s"}`;
}

export function relationErrorText(field: FieldDefinition, code: string): string {
  const relation = field.relation;
  if (code === "duplicate_relation_value") return "Related record IDs must be unique.";
  if (code === "relation_target_invalid") return "One or more related records do not exist or are unavailable.";
  if (code === "relation_min_select" && relation !== undefined) return `Select at least ${selectionLabel(relation.minSelect)}.`;
  if (code === "relation_max_select" && relation !== undefined) return `Select no more than ${selectionLabel(relation.maxSelect)}.`;
  if (code === "required") return "This relation is required.";
  if (code === "relation_invalid_value") {
    return relation?.cardinality === "multiple"
      ? "Enter one canonical, non-zero UUID per line."
      : "Enter a canonical, non-zero UUID.";
  }
  return "Relation value is invalid.";
}
