import type { CollectionDefinition, FieldDefinition, RecordData } from "../backend/ports";
import { canonicalRelationID } from "../workspace/record-codec";

export interface RelationCandidate { id: string; label: string }
export type RelationTargetSearch =
  | { filter: string; sort?: string; displayField?: FieldDefinition }
  | { problem: "invalid-uuid" | "too-long" };

const filterFieldPattern = /^[a-z_][a-z0-9_]*$/;
const maximumSearchBytes = 1024;

export function relationTargetSearchField(collection: CollectionDefinition): FieldDefinition | undefined {
  return collection.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .find((field) => field.type === "text" && (field.indexed || field.unique) && filterFieldPattern.test(field.name));
}

function quoted(value: string): string {
  return `'${value.replaceAll("'", "''")}'`;
}

/** Builds one exact, indexed lookup; the record port supplies the page bound. */
export function relationTargetSearch(collection: CollectionDefinition, query: string): RelationTargetSearch {
  const displayField = relationTargetSearchField(collection);
  if (displayField === undefined) {
    const id = canonicalRelationID(query);
    return id === null ? { problem: "invalid-uuid" } : { filter: `id = ${quoted(id)}` };
  }
  if (query.includes("\0") || new TextEncoder().encode(query).length > maximumSearchBytes) return { problem: "too-long" };
  return { displayField, filter: `${displayField.name} = ${quoted(query)}`, sort: displayField.name };
}

/** Ignores malformed server rows rather than putting an invalid id in a draft. */
export function relationTargetOption(record: RecordData, displayField?: FieldDefinition): RelationCandidate | undefined {
  const id = typeof record.id === "string" ? canonicalRelationID(record.id) : null;
  if (id === null) return undefined;
  const display = displayField === undefined ? undefined : record[displayField.name];
  return { id, label: typeof display === "string" && display !== "" ? display : id };
}
