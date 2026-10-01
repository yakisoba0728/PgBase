import type { CollectionDefinition, FieldDefinition, RecordData, RecordIDPolicy } from "../backend/ports";
import { canonicalRelationID, relationDraftMembers } from "../workspace/record-codec";

export interface RelationCandidate { id: string; label: string }
export interface RelationTargetPlan { filter?: string; sort?: string; displayField?: FieldDefinition }
export type RelationTargetSearch = RelationTargetPlan | { problem: "invalid-uuid" | "invalid-id" | "too-long" };

const filterFieldPattern = /^[a-z_][a-z0-9_]*$/;
const maximumSearchBytes = 1024;

// A hydration query names no more than 50 ids, and each response page remains
// inside the record port's fixed bounds. Four batches cap the selected ids one
// editor hydrates; a selection past that keeps its own ID, which is a stable
// label rather than a blank.
export const relationHydrationBatch = 50;
const relationHydrationRequests = 4;
export const relationHydrationLimit = relationHydrationBatch * relationHydrationRequests;

export function relationTargetSearchField(collection: CollectionDefinition): FieldDefinition | undefined {
	const ordered = collection.fields
    .slice()
		.sort((left, right) => left.ordinal - right.ordinal);
	return ordered.find((field) => field.presentable && !field.hidden && field.type === "text" && (field.indexed || field.unique) && filterFieldPattern.test(field.name))
		?? ordered.find((field) => !field.hidden && field.type === "text" && (field.indexed || field.unique) && filterFieldPattern.test(field.name));
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
    const id = canonicalRelationID(query, collection.recordId);
    return id === null ? { problem: collection.recordId?.kind === "text" ? "invalid-id" : "invalid-uuid" } : { filter: `id = ${quoted(id)}` };
  }
  if (query.includes("\0") || new TextEncoder().encode(query).length > maximumSearchBytes) return { problem: "too-long" };
  return { displayField, filter: `${displayField.name} = ${quoted(query)}`, sort: displayField.name };
}

/** Ignores malformed server rows rather than putting an invalid id in a draft. */
export function relationTargetOption(record: RecordData, displayField?: FieldDefinition, policy?: RecordIDPolicy): RelationCandidate | undefined {
  const id = typeof record.id === "string" ? canonicalRelationID(record.id, policy) : null;
  if (id === null) return undefined;
  const display = displayField === undefined ? undefined : record[displayField.name];
  return { id, label: typeof display === "string" && display !== "" ? display : id };
}

/** The ordered, unique, canonical ids a relation draft currently selects. */
export function relationDraftSelection(draft: string, policy?: RecordIDPolicy): string[] {
  const selected: string[] = [];
  for (const member of relationDraftMembers(draft, policy)) {
    const id = canonicalRelationID(member, policy);
    if (id !== null && !selected.includes(id)) selected.push(id);
  }
  return selected;
}

/** Drops one selection, leaving the order of the rest untouched. */
export function deselectRelationTarget(draft: string, selectedID: string, policy?: RecordIDPolicy): string {
  const id = canonicalRelationID(selectedID, policy);
  return relationDraftMembers(draft, policy).filter((member) => canonicalRelationID(member, policy) !== id).join("\n");
}

/** One primary-key query per hydration batch. */
export function relationHydrationFilter(ids: readonly string[]): string {
  return ids.map((id) => `id = ${quoted(id)}`).join(" || ");
}

/** Text IDs can occupy up to 1,020 UTF-8 bytes. Keep each query inside the
 * read kernel's 4,096-byte and 100-node limits as well as its row bound. */
export function relationHydrationBatchIDs(ids: readonly string[]): string[] {
  const batch: string[] = [];
  for (const id of ids.slice(0, relationHydrationBatch)) {
    if (new TextEncoder().encode(relationHydrationFilter([...batch, id])).length > 4096) break;
    batch.push(id);
  }
  return batch;
}

/** Apply the hydration budget to the current selection, so typing or choosing
 * a new ID later can still hydrate it without retrying previously claimed IDs. */
export function relationHydrationSelection(ids: readonly string[]): string[] {
  const selected: string[] = [];
  for (let batch = 0; batch < relationHydrationRequests; batch += 1) {
    const next = relationHydrationBatchIDs(ids.slice(selected.length, relationHydrationLimit));
    if (next.length === 0) break;
    selected.push(...next);
  }
  return selected;
}
