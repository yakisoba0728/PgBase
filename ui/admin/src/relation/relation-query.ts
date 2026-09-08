import type { CollectionDefinition, FieldDefinition, RecordData } from "../backend/ports";
import { canonicalRelationID, relationDraftMembers } from "../workspace/record-codec";

export interface RelationCandidate { id: string; label: string }
export interface RelationTargetPlan { filter?: string; sort?: string; displayField?: FieldDefinition }
export type RelationTargetSearch = RelationTargetPlan | { problem: "invalid-uuid" | "too-long" };

const filterFieldPattern = /^[a-z_][a-z0-9_]*$/;
const maximumSearchBytes = 1024;

// A hydration query names no more than 50 ids, and each response page remains
// inside the record port's fixed bounds. Four batches cap the selected ids one
// editor hydrates; a selection past that keeps its own UUID, which is a stable
// label rather than a blank.
export const relationHydrationBatch = 50;
export const relationHydrationLimit = relationHydrationBatch * 4;

export function relationTargetSearchField(collection: CollectionDefinition): FieldDefinition | undefined {
  return collection.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .find((field) => field.type === "text" && (field.indexed || field.unique) && filterFieldPattern.test(field.name));
}

function quoted(value: string): string {
  return `'${value.replaceAll("'", "''")}'`;
}

/**
 * Builds one page query the read kernel can serve from an index. A named field
 * only ever comes from `relationTargetSearchField`, which requires its own
 * index or unique constraint, and every other query addresses `id` or falls
 * back to the kernel's default `created_at, id` order -- both indexed. So the
 * picker has no way to express the unindexed scan the planner would refuse.
 * The record port supplies the page bound; the cursor supplies the rest.
 */
export function relationTargetSearch(collection: CollectionDefinition, query: string): RelationTargetSearch {
  const displayField = relationTargetSearchField(collection);
  // An empty query browses the target in its indexed order rather than
  // demanding an exact value the administrator has to already know.
  if (query.trim() === "") return displayField === undefined ? {} : { displayField, sort: displayField.name };
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

/** The ordered, unique, canonical ids a relation draft currently selects. */
export function relationDraftSelection(draft: string): string[] {
  const selected: string[] = [];
  for (const member of relationDraftMembers(draft)) {
    const id = canonicalRelationID(member);
    if (id !== null && !selected.includes(id)) selected.push(id);
  }
  return selected;
}

/** Drops one selection, leaving the order of the rest untouched. */
export function deselectRelationTarget(draft: string, selectedID: string): string {
  const id = canonicalRelationID(selectedID);
  return relationDraftMembers(draft).filter((member) => canonicalRelationID(member) !== id).join("\n");
}

/**
 * One primary-key query per hydration batch. Equality against `id` is the
 * cheapest predicate the kernel has, and a batch stays inside the filter's
 * 4,096-byte and 100-node budgets because 50 ids cost roughly 2,350 bytes and
 * 51 nodes.
 */
export function relationHydrationFilter(ids: readonly string[]): string {
  return ids.map((id) => `id = '${id}'`).join(" || ");
}
