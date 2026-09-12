import { useCallback, useEffect, useMemo, useRef } from "react";
import { useBackend, useSession } from "../auth/session";
import { parseJSON, stringifyJSON } from "../backend/json";
import {
  ApplyAttempt,
  CollectionCatalog,
  SchemaError,
  type CollectionChange,
  type CollectionDefinition,
  type AuthCollectionScaffold,
  type CompoundIndex,
  type FieldInput,
  type FieldType,
  type CollectionType,
  type PlannedChange,
  type PlannedField,
  type RelationCardinality,
  type RelationDeleteAction,
  type SchemaDocument,
  type SchemaDocumentCollection,
  type SchemaImportPlan,
  type ViewDefinition,
} from "../backend/ports";
import { fieldOptionsInput, draftFieldOptions, type DraftFieldOptions } from "./field-options";
import { operationFailureMessage, useOperationRecovery, type OperationRecovery } from "../operations/operation-recovery";
import { adminRouteIdentity, useResourceState, useRouteGuard, type AdminRoute } from "../routing/admin-location";
import { useUnsavedDraft } from "../routing/unsaved-draft";

interface DraftRelation {
  targetCollection: string; cardinality: RelationCardinality; minSelect: string;
  maxSelect: string; onDelete: RelationDeleteAction;
}

export interface DraftField {
  id?: string; options?: DraftFieldOptions;
  name: string; type: FieldType;
  required: boolean; unique: boolean; indexed: boolean;
  hidden?: boolean; presentable?: boolean; help?: string;
  system?: boolean;
  hasDefault: boolean; defaultText: string; relation?: DraftRelation;
  // Keeps an explicit self choice from being inferred from a half-typed name.
  selfRelation?: boolean;
}

export interface RelationTargetOption { name: string; label: string }

export type DraftEdit =
  | { set: "options"; value: DraftFieldOptions }
  | { set: "name" | "defaultText" | "target" | "minSelect" | "maxSelect" | "help"; value: string }
  | { set: "required" | "unique" | "indexed" | "hasDefault" | "hidden" | "presentable"; value: boolean }
  | { set: "type"; value: FieldType }
  | { set: "cardinality"; value: RelationCardinality } | { set: "onDelete"; value: RelationDeleteAction };

export type DesignerMode = { kind: "create" } | { kind: "edit" | "duplicate"; collection: string } | { kind: "scaffold"; scaffold: "base" | "auth" | "view" };

class DraftError extends Error {}

const dataBlockedText = "Existing data blocks this change (for example missing or duplicate values).";
const catalogMovedText = "The catalog changed. Reload this page before making another schema change.";
const appliedText = "The change was applied. The edits you made after sending it are not saved yet — leave and reopen the collection before applying them.";
const createdText = "The collection was created. The edits you made after sending it are not part of it and are not saved — open the collection from the list to apply them.";

const newRelation = (): DraftRelation => ({ targetCollection: "", cardinality: "single", minSelect: "0", maxSelect: "1", onDelete: "restrict" });
const newField = (): DraftField => ({ name: "", type: "text", required: false, unique: false, indexed: false, hidden: false, presentable: false, system: false, help: "", hasDefault: false, defaultText: "" });

function parseDefault(type: FieldType, value: string): unknown {
  if (type === "boolean") {
    if (value !== "true" && value !== "false") throw new DraftError("Enter true or false for the boolean default.");
    return value === "true";
  }
  if (type === "integer" || type === "bigint") {
    if (!/^-?[0-9]+$/.test(value)) throw new DraftError("Enter an integer default.");
    return value;
  }
  if (type === "numeric") {
    if (!/^-?[0-9]+(?:\.[0-9]+)?$/.test(value)) throw new DraftError("Enter a decimal default without exponent notation.");
    return value;
  }
  if (type === "timestamptz" && value === "now") return { kind: "now" };
  if (type === "jsonb") {
    try { return parseJSON(value); } catch { throw new DraftError("Enter valid JSON for the jsonb default."); }
  }
  return value;
}

function parseBound(value: string, fieldNumber: number, label: "minimum" | "maximum"): number {
  if (!/^(?:0|[1-9][0-9]*)$/.test(value)) throw new DraftError(`Field ${fieldNumber} relation ${label} must be a non-negative integer.`);
  return Number(value);
}

function relationInput(field: DraftField, index: number): FieldInput["relation"] {
  const relation = field.relation;
  const fieldNumber = index + 1;
  if (relation === undefined || relation.targetCollection === "") {
    throw new DraftError(`Choose a target collection for field ${fieldNumber}.`);
  }
  const minSelect = parseBound(relation.minSelect, fieldNumber, "minimum");
  const maxSelect = parseBound(relation.maxSelect, fieldNumber, "maximum");
  if (relation.cardinality === "single" && (minSelect > 1 || maxSelect !== 1)) {
    throw new DraftError(`Field ${fieldNumber} single relation must use minimum 0 or 1 and maximum 1.`);
  }
  if (relation.cardinality === "multiple" && (maxSelect < 2 || maxSelect > 1000 || minSelect > maxSelect)) {
    throw new DraftError(`Field ${fieldNumber} multiple relation must use maximum 2–1000 and minimum no greater than maximum.`);
  }
  if (field.required !== (minSelect > 0)) {
    throw new DraftError(`Field ${fieldNumber} required setting must match whether its relation minimum is greater than zero.`);
  }
  if (relation.onDelete === "set_null" && minSelect !== 0) {
    throw new DraftError(`Field ${fieldNumber} set-null relation must be optional.`);
  }
  if (relation.cardinality === "multiple" && field.unique) {
    throw new DraftError(`Field ${fieldNumber} multiple relation cannot be unique.`);
  }
  return { targetCollection: relation.targetCollection, cardinality: relation.cardinality, minSelect, maxSelect, onDelete: relation.onDelete };
}

export function buildInput(operation: "create" | "update", name: string, fields: DraftField[], indexes?: CompoundIndex[], view?: ViewDefinition, type?: CollectionType): CollectionChange {
  if (indexes !== undefined) {
    for (const index of indexes) {
      if (index.name.trim() === "" || index.fields.length < 2 || index.fields.length > 8) throw new DraftError("Each compound index needs a name and two to eight fields.");
      const known = new Set(fields.filter((field) => !["relation", "file", "jsonb", "geoPoint", "password"].includes(field.type) && (field.type !== "editor" || (Number(field.options?.maxSize) || 5 * 1024 * 1024) <= 2048)).map((field) => field.name));
      if (new Set(index.fields).size !== index.fields.length || index.fields.some((field) => !known.has(field))) throw new DraftError("Compound indexes can use each scalar field once.");
    }
  }
	if (view !== undefined) {
		if (operation === "update") {
			if (typeof view.sql !== "string") throw new DraftError("Only SQL view definitions can be updated.");
			return { operation, name, view };
		}
		return { operation, name, fields: [], view };
	}
	const fieldInputs = fields.map((field, index) => {
			if (Array.from(field.help ?? "").length > 300) throw new DraftError(`Field ${index + 1} help must be at most 300 characters.`);
			if (field.hidden && field.presentable) throw new DraftError(`Field ${index + 1} cannot be hidden and presentable.`);
			const base: FieldInput = { name: field.name, type: field.type, required: field.required, unique: field.unique, indexed: field.indexed };
			if (field.system) base.system = true;
			if (field.hidden) base.hidden = true;
			if (field.presentable) base.presentable = true;
			if (field.help) base.help = field.help;
			if (operation === "update" && field.id) base.id = field.id;
			const options = fieldOptionsInput(field.options) ?? (field.id ? {} : undefined);
			if (options !== undefined) base.options = options;
			if (field.type === "relation") return { ...base, relation: relationInput(field, index) };
			return field.hasDefault ? { ...base, default: parseDefault(field.type, field.defaultText) } : base;
	});
	return { operation, name, fields: fieldInputs, ...(indexes === undefined ? {} : { indexes }), ...(type === undefined ? {} : { type }) };
}

export function previewField(field: PlannedField): string {
  const flags = [
    field.required && "required",
    field.unique && "unique",
    field.indexed && "indexed",
    field.default !== undefined && `default: ${typeof field.default === "string" ? field.default : stringifyJSON(field.default)}`,
  ].filter(Boolean);
  if (field.relation !== undefined) {
    flags.push(
      `target: ${field.relation.targetCollection}`,
      field.relation.cardinality,
      `min: ${field.relation.minSelect}`,
      `max: ${field.relation.maxSelect}`,
      `on delete: ${field.relation.onDelete}`,
    );
  }
  if (field.options && Object.keys(field.options).length) flags.push(`validation: ${JSON.stringify(field.options)}`);
  return `${field.name} · ${field.type}${flags.length > 0 ? ` · ${flags.join(" · ")}` : ""}`;
}

function defaultTextFor(type: string, value: unknown): string {
  if (value === undefined) return "";
  if (type === "timestamptz" && value !== null && typeof value === "object" && (value as { kind?: string }).kind === "now") return "now";
  if (type === "jsonb") return stringifyJSON(value);
  return typeof value === "string" ? value : stringifyJSON(value);
}

function draftFields(definition: CollectionDefinition, catalog: CollectionCatalog): DraftField[] {
  const names = new Map(catalog.all().filter((entry) => entry.id !== undefined).map((entry) => [entry.id as string, entry.name]));
  return definition.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .map((field) => ({
      id: field.id, options: draftFieldOptions(field.options),
      name: field.name,
      type: field.type,
      required: field.required,
      unique: field.unique,
      indexed: field.indexed,
      hidden: field.hidden, presentable: field.presentable, help: field.help,
			system: field.system,
      hasDefault: field.default !== undefined && !["editor", "geoPoint", "autodate", "password"].includes(field.type),
      defaultText: defaultTextFor(field.type, field.default),
      relation: field.relation === undefined ? undefined : {
        targetCollection: names.get(field.relation.targetCollectionId) ?? "",
        cardinality: field.relation.cardinality,
        minSelect: String(field.relation.minSelect),
        maxSelect: String(field.relation.maxSelect),
        onDelete: field.relation.onDelete,
      },
    }));
}

function scaffoldFields(fields: FieldInput[] | undefined): DraftField[] {
	if (fields === undefined || fields.length === 0) return [newField()];
	return fields.map((field) => ({ options: draftFieldOptions(field.options), name: field.name, type: field.type, required: field.required, unique: field.unique, indexed: field.indexed, hidden: field.hidden, presentable: field.presentable, system: field.system, help: field.help, hasDefault: field.default !== undefined && !["editor", "geoPoint", "autodate", "password"].includes(field.type), defaultText: defaultTextFor(field.type, field.default), relation: field.relation === undefined ? undefined : { targetCollection: field.relation.targetCollection, cardinality: field.relation.cardinality, minSelect: String(field.relation.minSelect), maxSelect: String(field.relation.maxSelect), onDelete: field.relation.onDelete } }));
}

function withType(field: DraftField, type: FieldType): DraftField {
  if (type !== "relation") {
    const fixed = ["file", "editor", "geoPoint", "autodate", "password"].includes(type);
    return { ...field, type, options: type === "autodate" ? { onCreate: "true", onUpdate: "true" } : undefined, relation: undefined, selfRelation: false,
      ...(fixed ? { hasDefault: false, defaultText: "", unique: false, indexed: false } : {}),
      ...(type === "autodate" ? { required: false } : {}), ...(type === "password" ? { presentable: false } : {}) };
  }
  const relation = field.relation ?? newRelation();
  return {
    ...field,
    type,
    options: undefined,
    indexed: true,
    hasDefault: false,
    defaultText: "",
    selfRelation: false,
    unique: relation.cardinality === "single" && field.unique,
    required: Number(relation.minSelect) > 0,
    relation,
  };
}

function applyEdit(field: DraftField, edit: DraftEdit, collectionName: string): DraftField {
  if (field.system) return field;
  const relation = field.relation;
  if (edit.set === "options") return { ...field, options: edit.value, ...(field.type === "editor" && (Number(edit.value?.maxSize) || 5 * 1024 * 1024) > 2048 ? { indexed: false, unique: false } : {}) };
  if (edit.set === "name") return { ...field, name: edit.value };
  if (edit.set === "defaultText") return { ...field, defaultText: edit.value };
  if (edit.set === "hasDefault") return { ...field, hasDefault: edit.value };
  if (edit.set === "unique") return { ...field, unique: edit.value };
  if (edit.set === "indexed") return { ...field, indexed: edit.value };
	if (edit.set === "help") return { ...field, help: edit.value };
	if (edit.set === "hidden") return { ...field, hidden: edit.value, presentable: edit.value ? false : field.presentable };
	if (edit.set === "presentable") return { ...field, presentable: edit.value };
  if (edit.set === "type") return withType(field, edit.value);
  if (edit.set === "required") {
    if (relation === undefined) return { ...field, required: edit.value };
    return { ...field, required: edit.value, relation: { ...relation, minSelect: edit.value ? (Number(relation.minSelect) > 0 ? relation.minSelect : "1") : "0" } };
  }
  if (relation === undefined) return field;
  if (edit.set === "target") {
    return { ...field, selfRelation: edit.value !== "" && edit.value === collectionName, relation: { ...relation, targetCollection: edit.value } };
  }
  if (edit.set === "maxSelect") return { ...field, relation: { ...relation, maxSelect: edit.value } };
  if (edit.set === "minSelect") {
    return { ...field, required: /^(?:0|[1-9][0-9]*)$/.test(edit.value) && Number(edit.value) > 0, relation: { ...relation, minSelect: edit.value } };
  }
  if (edit.set === "cardinality") {
    return {
      ...field,
      unique: edit.value === "single" && field.unique,
      relation: {
        ...relation,
        cardinality: edit.value,
        minSelect: field.required ? "1" : "0",
        maxSelect: edit.value === "single" ? "1" : relation.cardinality === "multiple" ? relation.maxSelect : "20",
      },
    };
  }
  if (edit.set === "onDelete") {
    return {
      ...field,
      required: edit.value === "set_null" ? false : field.required,
      relation: { ...relation, onDelete: edit.value, minSelect: edit.value === "set_null" ? "0" : relation.minSelect },
    };
  }
  return field;
}

function relationTargets(catalog: CollectionCatalog | null, sourceCollection: string): RelationTargetOption[] {
  const names = new Set((catalog?.all() ?? []).map((collection) => collection.name));
  if (sourceCollection !== "") names.add(sourceCollection);
  return [...names]
    .sort((left, right) => left.localeCompare(right))
    .map((name) => ({ name, label: name === sourceCollection ? `${name} (self)` : name }));
}

function draftMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function schemaMessage(error: unknown, fallback: string, validationFallback = "Collection input is invalid."): string {
  if (!(error instanceof SchemaError)) return fallback;
  const detailPrefix = error.code === "collection_referenced" ? "Collection is referenced." : validationFallback;
  const details = Object.entries(error.details).flatMap(([path, codes]) => codes.map((code) => `${path}: ${code}`)).sort();
  if (details.length > 0) return `${detailPrefix} ${details.join("; ")}`;
  if (error.code === "validation_failed") return validationFallback;
  if (error.code !== "request_failed" && error.code !== "schema_conflict") return `${fallback} (${error.code})`;
  return fallback;
}

// The whole last-agreed draft distinguishes an untouched form.
const draftSignature = (name: string, fields: DraftField[], indexes?: CompoundIndex[], view?: ViewDefinition, type?: CollectionType): string => stringifyJSON({ name, fields, indexes, view, type });

interface State {
  identity: string; create: boolean; phase: "loading" | "missing" | "unavailable" | "ready";
  name: string; tableName: string; definition: CollectionDefinition | null; catalog: CollectionCatalog | null;
	collectionType?: CollectionType;
	authBlueprint?: AuthCollectionScaffold;
	duplicateSource?: string; scaffold?: "base" | "auth" | "view";
  targetsLoading: boolean; targetsFailed: boolean; fields: DraftField[];
  indexes?: CompoundIndex[];
  view?: ViewDefinition;
  plan: PlannedChange | null; authPlan: AuthImportPlan | null; confirmed: boolean; pending: boolean; error: string | null;
  deletePlan: PlannedChange | null; deleteConfirmText: string; deletePending: boolean; deleteError: string | null;
  pristine: string; undecided: UndecidedApply | null; terminal: boolean;
}
interface CollectionPreview { baseVersion: number; fields: readonly PlannedField[]; sql: readonly string[] }
interface AuthImportPlan extends CollectionPreview { plan: SchemaImportPlan; signature: string }
type UndecidedApply = { kind: "change" | "drop"; operationId: string; draft: string | null; expiresAt: number };
// A server apply can run for one hour; five extra minutes allow transaction/reconciliation settlement.
const recoverabilityWindowMs = 65 * 60 * 1000;
const recoverabilityText = "Unable to use browser storage safely for this operation. Restore it and reload before applying.";
const operationIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const legacyPendingKey = (administratorId: string, route: string) => `pgbase.schema-operation.v1.${administratorId}.${encodeURIComponent(route)}`;
const pendingPrefix = (administratorId: string, route: string) => `pgbase.schema-operation.v2.${administratorId}.${encodeURIComponent(route)}.`;
const pendingKey = (administratorId: string, route: string, operationId: string) => `${pendingPrefix(administratorId, route)}${operationId}`;
function storedPending(administratorId: string, route: string): UndecidedApply | null | false {
  try {
    const prefix = pendingPrefix(administratorId, route), legacyKey = legacyPendingKey(administratorId, route);
    const keys = [legacyKey, ...Array.from({ length: localStorage.length }, (_, index) => localStorage.key(index)).filter((key): key is string => key?.startsWith(prefix) === true)];
    const candidates: UndecidedApply[] = [], now = Date.now();
    for (const key of new Set(keys)) {
      const raw = localStorage.getItem(key); if (raw === null) continue; const value = parseJSON(raw) as Partial<UndecidedApply> | null;
      if ((value?.kind !== "change" && value?.kind !== "drop") || typeof value.operationId !== "string" || !operationIDPattern.test(value.operationId) ||
        typeof value.expiresAt !== "number" || !Number.isSafeInteger(value.expiresAt) || value.expiresAt > now + recoverabilityWindowMs ||
        (key !== legacyKey && key !== pendingKey(administratorId, route, value.operationId))) return false;
      if (value.expiresAt <= now) localStorage.removeItem(key); else candidates.push({ kind: value.kind, operationId: value.operationId, expiresAt: value.expiresAt, draft: null });
    }
    candidates.sort((left, right) => left.expiresAt - right.expiresAt || left.operationId.localeCompare(right.operationId)); return candidates[0] ?? null;
  } catch { return false; }
}
function rememberPending(administratorId: string, route: string, pending: UndecidedApply): boolean {
  try { const key = pendingKey(administratorId, route, pending.operationId), value = stringifyJSON({ kind: pending.kind, operationId: pending.operationId, expiresAt: pending.expiresAt });
    localStorage.setItem(key, value); return localStorage.getItem(key) === value; } catch { return false; }
}
function forgetPending(administratorId: string | null, route: string, operationId: string): boolean {
  if (administratorId === null) return false;
  try { localStorage.removeItem(pendingKey(administratorId, route, operationId)); const legacyKey = legacyPendingKey(administratorId, route), legacy = localStorage.getItem(legacyKey);
    if (legacy !== null && (parseJSON(legacy) as Partial<UndecidedApply> | null)?.operationId === operationId) localStorage.removeItem(legacyKey);
    return true; } catch { return false; }
}
function initial(mode: DesignerMode, identity: string): State {
  const create = mode.kind !== "edit";
  const name = create ? "" : mode.collection;
  const fields = create ? [newField()] : [];
  return {
    identity, create, phase: mode.kind === "create" ? "ready" : "loading", name, tableName: "",
    definition: null, catalog: null, targetsLoading: false, targetsFailed: false, fields, indexes: create ? [] : undefined,
		...(mode.kind === "duplicate" ? { duplicateSource: mode.collection } : {}),
		...(mode.kind === "scaffold" ? { scaffold: mode.scaffold } : {}),
    plan: null, authPlan: null, confirmed: false, pending: false, error: null,
    deletePlan: null, deleteConfirmText: "", deletePending: false, deleteError: null,
    collectionType: mode.kind === "scaffold" ? mode.scaffold : undefined,
    pristine: draftSignature(name, fields, create ? [] : undefined, undefined, mode.kind === "scaffold" ? mode.scaffold : undefined), undecided: null, terminal: false,
  };
}

const redrafted = (fields: DraftField[]): Partial<State> => ({ fields, plan: null, authPlan: null, confirmed: false });

function loadedState(current: State, catalog: CollectionCatalog, scaffolds?: { base: SchemaDocumentCollection; auth: AuthCollectionScaffold; view: SchemaDocumentCollection }, document?: SchemaDocument): Partial<State> {
  const base = { catalog, targetsLoading: false, targetsFailed: false };
  if (current.create) {
		const source = current.duplicateSource === undefined ? undefined : catalog.get(current.duplicateSource);
		if (current.duplicateSource !== undefined && source === undefined) return { ...base, phase: "missing" };
		const requested = current.scaffold === undefined ? undefined : scaffolds?.[current.scaffold];
		if (current.scaffold !== undefined && requested === undefined) return { ...base, phase: "unavailable" };
		// A plain create only loads this catalog to resolve a relation target. It
		// has no template to hydrate, so keep the editable draft intact.
		if (source === undefined && current.scaffold === undefined) return base;
		const auth = current.scaffold === "auth" || source?.type === "auth";
		if (!auth && source !== undefined) {
			const fields = draftFields(source, catalog), indexes = (source.indexes ?? []).map((index) => ({ name: index.name, fields: [...index.fields], unique: index.unique })), view = source.view !== undefined && "sql" in source.view ? { sql: source.view.sql } as ViewDefinition : undefined, name = `${source.name}_copy`, collectionType = source.type ?? (view === undefined ? "base" : "view");
			return { ...base, phase: "ready", name, fields, indexes, view, collectionType, pristine: draftSignature(name, fields, indexes, view, collectionType) };
		}
		const template = auth
			? current.scaffold === "auth" ? scaffolds?.auth.collection : document?.collections.find((item) => item.name === source?.name)
			: requested as SchemaDocumentCollection | undefined;
		const authBlueprint = auth
			? current.scaffold === "auth" ? scaffolds?.auth : (() => {
				const authCollection = document?.authCollections?.find((item) => item.name === source?.name);
				return template === undefined || authCollection === undefined ? undefined : { collection: template, authCollection };
			})()
			: undefined;
		if (template === undefined || (auth && authBlueprint === undefined)) return { ...base, phase: "unavailable" };
		const fields = scaffoldFields(template.fields), indexes = (template.indexes ?? []).map((index) => ({ name: index.name, fields: [...index.fields], unique: index.unique })), view = template.view, name = source === undefined ? "" : `${source.name}_copy`, collectionType = template.type ?? (view === undefined ? "base" : "view");
		return { ...base, phase: "ready", name, fields, indexes, view, collectionType, authBlueprint, pristine: draftSignature(name, fields, indexes, view, collectionType) };
	}
  const definition = catalog.get(current.name);
  if (definition === undefined) return { ...base, phase: "missing" };
	const fields = draftFields(definition, catalog);
	const view = definition.view !== undefined && "sql" in definition.view ? { sql: definition.view.sql } as ViewDefinition : undefined;
	return {
		...base, phase: "ready", definition, name: definition.name, tableName: definition.tableName, fields,
		indexes: undefined, view, collectionType: definition.type, pristine: draftSignature(definition.name, fields, undefined, view, definition.type),
  };
}

function authImportDocument(state: State): SchemaDocument {
  if (state.authBlueprint === undefined) throw new DraftError("The authentication template is unavailable.");
  const change = buildInput("create", state.name, state.fields, state.indexes, undefined, "auth");
  if (!("fields" in change) || change.fields === undefined) throw new DraftError("Authentication fields are unavailable.");
  const collection = {
    ...state.authBlueprint.collection,
    name: state.name,
    type: "auth" as const,
    fields: change.fields,
    ...(change.indexes === undefined ? {} : { indexes: change.indexes }),
  };
  const authCollection = {
    ...state.authBlueprint.authCollection,
    name: state.name,
    // The auth authority stores custom fields only; its system fields remain
    // in the catalog collection and are joined by userauth at runtime.
    fields: change.fields.filter((field) => field.system !== true),
  };
  return { format: "pgbase.schema/v1", collections: [collection], authCollections: [authCollection] };
}

function renamed(current: State, name: string): Partial<State> {
  return {
    name, plan: null, authPlan: null, confirmed: false,
    fields: current.fields.map((field) => field.selfRelation === true && field.relation !== undefined
      ? { ...field, relation: { ...field.relation, targetCollection: name } }
      : field),
  };
}

interface DeleteTarget { collection: string; tableName: string; baseVersion: number; sql: readonly string[] }

function deleteTargetOf(definition: CollectionDefinition | null, plan: PlannedChange | null): DeleteTarget | null {
  if (definition === null || plan === null) return null;
  if (plan.operation !== "delete" || plan.collection !== definition.name) return null;
  return { collection: definition.name, tableName: definition.tableName, baseVersion: plan.baseVersion, sql: plan.sql };
}

export interface CollectionDesigner {
  phase: State["phase"]; create: boolean; name: string; tableName: string; fields: DraftField[];
  relationTargets: RelationTargetOption[]; relationTargetsLoading: boolean; relationTargetsFailed: boolean; isNewField(name: string): boolean;
  collectionType: CollectionType; preview: CollectionPreview | null; removedFields: string[]; destructiveConfirmed: boolean;
  applyBlocked: boolean; editingBlocked: boolean; pending: boolean; error: string | null;
  deleteTarget: DeleteTarget | null; deleteConfirmText: string; deleteConfirmed: boolean;
  deletePending: boolean; deleteError: string | null;
  recovery: OperationRecovery | null;
	indexes: CompoundIndex[]; indexesTouched: boolean; setIndexes(indexes: CompoundIndex[]): void; view?: ViewDefinition; setView(view?: ViewDefinition): void; viewSources: CollectionDefinition[]; isView: boolean; isSQLView: boolean;
  rename(name: string): void; editField(index: number, edit: DraftEdit): void; addField(): void; removeField(index: number): void;
  confirmDestructive(confirmed: boolean): void; confirmDeleteName(typed: string): void; requestPreview(): void; requestApply(): void;
  requestDeletePlan(): void; cancelDelete(): void; requestDelete(): void;
}

const operationFailed = (caught: unknown, fallback: string): string => operationFailureMessage(caught instanceof SchemaError ? caught.details.operation?.[0] : undefined, fallback);

function applyFailure(caught: unknown, create: boolean): Partial<State> {
  const reason = caught instanceof SchemaError ? caught.reason : "failed";
  if (reason === "operation-conflict") {
    return {
      error: "This change was already sent under a different plan. Preview the change again before applying.",
      plan: null, confirmed: false,
    };
  }
  if (reason === "operation-failed") {
    return { error: operationFailed(caught, "The schema operation did not apply."), plan: null, confirmed: false };
  }
  if (reason === "same-version-conflict") {
    return {
      error: create
        ? "A collection with this name likely already exists."
        : "The plan conflicts with the current state. Preview again; if it keeps failing, existing data may be blocking it.",
      plan: null,
      confirmed: false,
    };
  }
  if (reason === "catalog-moved") return { error: catalogMovedText, plan: null, confirmed: false };
  if (!create && reason === "data-blocked") return { error: schemaMessage(caught, dataBlockedText, dataBlockedText) };
  return { error: schemaMessage(caught, create ? "Unable to create the collection." : "Unable to apply the collection change.") };
}

function deleteFailure(caught: unknown): Partial<State> {
  const reason = caught instanceof SchemaError ? caught.reason : "failed";
  if (reason === "operation-conflict") {
    return { deletePlan: null, deleteConfirmText: "", deleteError: "This deletion was already sent under a different plan. Preview the deletion again." };
  }
  if (reason === "operation-failed") {
    return { deletePlan: null, deleteConfirmText: "", deleteError: operationFailed(caught, "The schema operation did not apply.") };
  }
  return {
    deletePlan: null,
    deleteConfirmText: "",
    deleteError: reason === "referenced" ? schemaMessage(caught, "Unable to delete the collection.", "Collection is referenced.")
      : reason === "catalog-moved" ? catalogMovedText
        : schemaMessage(caught, "Unable to delete the collection."),
  };
}

export function useCollectionDesigner(mode: DesignerMode, navigate: (route: AdminRoute) => void): CollectionDesigner {
  const { schema } = useBackend();
  const session = useSession().state;
  const administratorId = session.kind === "authenticated" ? session.administrator.id : null;
  const identity = adminRouteIdentity(mode.kind === "create" ? { kind: "collection-create" }
	: mode.kind === "duplicate" ? { kind: "collection-duplicate", collection: mode.collection }
	: mode.kind === "scaffold" ? { kind: "collection-scaffold", scaffold: mode.scaffold }
	: { kind: "collection-edit", collection: mode.collection });
  const active = useRouteGuard(identity);
  const [state, update] = useResourceState(identity, (current) => initial(mode, current));
  // Retries reuse one identity per approved plan; a fresh preview does not.
  const attempt = useRef(new ApplyAttempt());
  const issued = useRef<string | null>(null);
  const stored = administratorId === null || state.pending || issued.current === identity ? null : storedPending(administratorId, identity);
  const storageBlocked = stored === false, restored = stored || null;
  const undecided = state.undecided ?? restored;
  const recovery = useOperationRecovery(undecided?.operationId ?? null);
  const signature = draftSignature(state.name, state.fields, state.indexes, state.view, state.collectionType);
  const live = useRef(signature);
  live.current = signature;
  const recovered = recovery?.status === "committed" ? undecided : null;
  const recoveryFailed = recovery?.status === "failed" && undecided !== null;
  const editingBlocked = state.terminal || storageBlocked || (undecided !== null && recovery?.status !== "failed");
  const ledgerOutran = recovered?.kind === "change" && recovered.draft !== null && recovered.draft !== signature;
  let unsaved = signature !== state.pristine;
  if (undecided?.kind === "change" && recovery !== null && undecided.draft !== null) {
    if (recovery.status === "committed") unsaved = signature !== undecided.draft;
    else if (recovery.undecided) unsaved = true;
  }
  const savedDraft = useUnsavedDraft(identity, unsaved);

  const wantsCatalog = state.catalog === null && !state.targetsFailed && state.phase !== "missing" && state.phase !== "unavailable" &&
    (!state.create || state.duplicateSource !== undefined || state.scaffold !== undefined || state.view !== undefined || state.fields.some((field) => field.type === "relation"));

  useEffect(() => {
    if (!wantsCatalog) return;
    const controller = new AbortController();
    update(identity, { targetsLoading: true });
    const exported = state.duplicateSource === undefined ? Promise.resolve(undefined) : schema.exportDocument?.(controller.signal);
    void Promise.all([schema.catalog(controller.signal), state.scaffold === undefined ? Promise.resolve(undefined) : schema.scaffolds?.(controller.signal), exported])
      .then(([catalog, scaffolds, document]) => { if (!controller.signal.aborted) update(identity, (current) => loadedState(current, catalog, scaffolds, document)); })
      .catch(() => {
        if (controller.signal.aborted) return;
        update(identity, (current) => current.create ? { targetsLoading: false, targetsFailed: true } : { phase: "unavailable" });
      });
    return () => controller.abort();
  }, [identity, schema, state.duplicateSource, state.scaffold, update, wantsCatalog]);

  useEffect(() => {
    if (undecided === null || recovery?.status === "committed" || recovery?.status === "failed") return;
    const expire = () => { if (forgetPending(administratorId, identity, undecided.operationId)) { issued.current = null; const next = administratorId === null ? null : storedPending(administratorId, identity); update(identity, { undecided: next || null, plan: null, confirmed: false, deletePlan: null, deleteConfirmText: "" }); } };
    const delay = undecided.expiresAt - Date.now();
    if (delay <= 0) { expire(); return; }
    const timer = window.setTimeout(expire, delay);
    return () => window.clearTimeout(timer);
  }, [administratorId, identity, recovery?.status, undecided, update]);

  useEffect(() => {
    if (recovery?.status === "failed" && undecided !== null && forgetPending(administratorId, identity, undecided.operationId)) { issued.current = null; const next = administratorId === null ? null : storedPending(administratorId, identity), message = operationFailureMessage(recovery.failureCode, "The schema operation did not apply."); if (next === false) update(identity, { undecided: null }); else if (next !== null) update(identity, { undecided: next, ...(undecided.kind === "drop" ? { deleteError: message } : { error: message }) }); else if (state.undecided === null) update(identity, { undecided }); }
    if (recovered === null) return;
    forgetPending(administratorId, identity, recovered.operationId);
    if (recovered.draft !== null && signature === recovered.draft) savedDraft();
    navigate(recovered.kind === "drop" || state.create
      ? { kind: "collections" }
      : { kind: "records", collection: state.definition?.name ?? state.name });
  }, [administratorId, identity, navigate, recovered, recovery?.status, savedDraft, signature, state.create, state.definition?.name, state.name, state.undecided, update]);

  const input = useMemo(() => {
    if (!state.create && state.definition === null) return null;
    try { return buildInput(state.create ? "create" : "update", state.name, state.fields, state.indexes, state.view, state.collectionType); } catch { return null; }
  }, [state.create, state.definition, state.fields, state.indexes, state.view, state.name, state.collectionType]);
  const preview = state.plan !== null && input !== null && state.plan.matches(input) ? state.plan : null;
  const authPreview = state.authPlan !== null && state.authPlan.signature === signature ? state.authPlan : null;
  const previewDetails = authPreview ?? preview;
  const removedFields = useMemo(() => {
    if (previewDetails === null || state.definition === null) return [];
    const planned = new Set(previewDetails.fields.map((field) => field.id ?? field.name));
    return state.definition.fields.filter((field) => !planned.has(field.id ?? field.name)).sort((left, right) => left.ordinal - right.ordinal).map((field) => field.name);
  }, [previewDetails, state.definition]);
  const existing = useMemo(() => new Set(state.definition?.fields.map((field) => field.name) ?? []), [state.definition]);

  const requestPreview = useCallback(async () => {
    if (editingBlocked || state.pending || (!state.create && state.definition === null)) return;
    if (state.create && state.collectionType === "auth") {
      if (schema.previewImport === undefined) { update(identity, { error: "This server cannot preview an authentication collection.", authPlan: null }); return; }
      let document: SchemaDocument;
      try { document = authImportDocument(state); }
      catch (caught: unknown) { update(identity, { error: draftMessage(caught, "Authentication collection input is invalid."), authPlan: null }); return; }
      update(identity, { error: null, pending: true, plan: null, authPlan: null });
      try {
        const planned = await schema.previewImport(document);
        const definition = planned.changes.find((change) => change.definition.name === state.name)?.definition;
        update(identity, { authPlan: { plan: planned, signature, baseVersion: planned.baseVersion, fields: definition?.fields ?? [], sql: planned.changes.flatMap((change) => change.sql) }, confirmed: false, deletePlan: null, deleteConfirmText: "" });
      } catch (caught: unknown) {
        update(identity, { error: schemaMessage(caught, "Unable to preview the authentication collection."), authPlan: null });
      } finally { update(identity, { pending: false }); }
      return;
    }
    let change: CollectionChange;
    try { change = buildInput(state.create ? "create" : "update", state.name, state.fields, state.indexes, state.view, state.collectionType); }
    catch (caught: unknown) { update(identity, { error: draftMessage(caught, "Collection input is invalid.") }); return; }
    update(identity, { error: null, pending: true });
    try {
      const planned = await schema.plan(change);
      if (!state.create && planned.baseVersion !== state.catalog?.version) {
        update(identity, { error: catalogMovedText, plan: null, confirmed: false });
        return;
      }
      update(identity, { plan: planned, authPlan: null, confirmed: false, deletePlan: null, deleteConfirmText: "", ...(recoveryFailed ? { undecided: null } : {}) });
    } catch (caught: unknown) {
      const fallback = state.create ? "Unable to preview the collection." : "Unable to preview the collection change.";
      update(identity, { error: schemaMessage(caught, fallback), plan: null, authPlan: null, confirmed: false });
    } finally {
      update(identity, { pending: false });
    }
  }, [editingBlocked, identity, recoveryFailed, schema, signature, state, update]);

  const requestApply = useCallback(async () => {
    if (editingBlocked || recoveryFailed || previewDetails === null || state.pending || (removedFields.length > 0 && !state.confirmed)) return;
    if (authPreview !== null) {
      if (schema.applyImport === undefined) { update(identity, { error: "This server cannot apply an authentication collection." }); return; }
      update(identity, { pending: true, error: null });
      try {
        const result = await schema.applyImport(authPreview.plan);
        if (!active()) return;
        if (result.status !== "committed") { update(identity, { error: `Authentication collection import ${result.status}. Check schema operations before retrying.` }); return; }
        update(identity, { pristine: signature, terminal: true, authPlan: null });
        if (live.current === signature) savedDraft();
        navigate({ kind: "collections" });
      } catch (caught: unknown) {
        update(identity, { error: schemaMessage(caught, "Unable to create the authentication collection.") });
      } finally { update(identity, { pending: false }); }
      return;
    }
    if (preview === null) return;
    update(identity, { pending: true, error: null, undecided: null });
    issued.current = identity;
    const pending = { kind: "change" as const, operationId: attempt.current.identityFor(preview).operationId, draft: signature, expiresAt: Date.now() + recoverabilityWindowMs };
    if (administratorId === null || !rememberPending(administratorId, identity, pending)) {
      issued.current = null; update(identity, { pending: false, error: recoverabilityText });
      return;
    }
    try {
      const outcome = await schema.apply(preview, attempt.current);
      if (outcome.kind === "applied") forgetPending(administratorId, identity, pending.operationId);
      if (!active()) return;
      if (outcome.kind === "undecided") { update(identity, { undecided: pending }); return; }
      const outran = live.current !== signature;
      const catalog = !state.create && outcome.definition !== undefined
        ? new CollectionCatalog(preview.baseVersion + 1, state.catalog?.all().map((collection) => collection.name === state.definition?.name ? outcome.definition! : collection) ?? [outcome.definition])
        : state.catalog;
      update(identity, {
        pristine: signature,
        catalog,
        definition: state.create ? null : outcome.definition ?? state.definition,
        terminal: state.create || outcome.definition === undefined,
        error: outran ? (state.create ? createdText : appliedText) : null,
      });
      // Only a response-supplied baseline can keep an outrunning edit draft.
      if (outran && !state.create && outcome.definition !== undefined) return;
      if (!outran) savedDraft();
      navigate(state.create ? { kind: "collections" } : { kind: "records", collection: state.definition?.name ?? state.name });
    } catch (caught: unknown) {
      forgetPending(administratorId, identity, pending.operationId);
      update(identity, applyFailure(caught, state.create));
    } finally { issued.current = null; update(identity, { pending: false }); }
  }, [active, administratorId, authPreview, editingBlocked, identity, navigate, preview, previewDetails, recoveryFailed, removedFields.length, savedDraft, schema, signature, state.confirmed, state.create, state.definition, state.name, state.pending, update]);

  const deleteTarget = useMemo(() => recoveryFailed ? null : deleteTargetOf(state.definition, state.deletePlan), [recoveryFailed, state.definition, state.deletePlan]);

  const requestDeletePlan = useCallback(async () => {
    if (editingBlocked || state.definition === null || state.deletePending) return;
    update(identity, { deletePending: true, deletePlan: null, deleteConfirmText: "", deleteError: null });
    try {
      const planned = await schema.plan({ operation: "delete", name: state.definition.name });
      if (planned.baseVersion !== state.catalog?.version) { update(identity, { deleteError: catalogMovedText }); return; }
      update(identity, {
        deletePlan: planned,
        plan: null, confirmed: false,
        ...(recoveryFailed ? { undecided: null } : {}),
      });
    } catch (caught: unknown) {
      update(identity, { deletePlan: null, deleteError: schemaMessage(caught, "Unable to preview the collection deletion.") });
    } finally {
      update(identity, { deletePending: false });
    }
  }, [editingBlocked, identity, recoveryFailed, schema, state.definition, state.deletePending, update]);

  const requestDelete = useCallback(async () => {
    if (editingBlocked || recoveryFailed || deleteTarget === null || state.deletePlan === null || state.deletePending) return;
    if (state.deleteConfirmText !== deleteTarget.collection) return;
    update(identity, { deletePending: true, undecided: null });
    issued.current = identity;
    const pending = { kind: "drop" as const, operationId: attempt.current.identityFor(state.deletePlan).operationId, draft: signature, expiresAt: Date.now() + recoverabilityWindowMs };
    if (administratorId === null || !rememberPending(administratorId, identity, pending)) {
      issued.current = null; update(identity, { deletePending: false, deleteError: recoverabilityText });
      return;
    }
    try {
      const outcome = await schema.apply(state.deletePlan, attempt.current);
      if (outcome.kind === "applied") forgetPending(administratorId, identity, pending.operationId);
      if (!active()) return;
      if (outcome.kind === "undecided") { update(identity, { undecided: pending }); return; }
      update(identity, { terminal: true, deletePlan: null, deleteConfirmText: "" });
      if (live.current === signature) savedDraft();
      navigate({ kind: "collections" });
    } catch (caught: unknown) {
      forgetPending(administratorId, identity, pending.operationId);
      update(identity, deleteFailure(caught));
    } finally { issued.current = null; update(identity, { deletePending: false }); }
  }, [active, administratorId, deleteTarget, editingBlocked, identity, navigate, recoveryFailed, savedDraft, schema, signature, state.deleteConfirmText, state.deletePending, state.deletePlan, update]);

  return {
    phase: state.phase,
    create: state.create,
    name: state.name,
    tableName: state.tableName,
    fields: state.fields,
    indexes: state.indexes ?? state.definition?.indexes ?? [],
    indexesTouched: state.indexes !== undefined,
    view: state.view,
    viewSources: (state.catalog?.all() ?? []).filter((collection) => collection.view === undefined),
		collectionType: state.collectionType ?? (state.view === undefined ? "base" : "view"),
    isView: state.definition?.view !== undefined,
		isSQLView: state.definition?.view !== undefined && "sql" in state.definition.view,
    relationTargets: relationTargets(state.catalog, state.name),
    relationTargetsLoading: state.targetsLoading,
    relationTargetsFailed: state.targetsFailed,
    isNewField: (name) => !state.create && !existing.has(name),
    preview: previewDetails,
    removedFields,
    destructiveConfirmed: state.confirmed,
    applyBlocked: editingBlocked || recoveryFailed || (removedFields.length > 0 && !state.confirmed),
    editingBlocked,
    pending: state.pending,
    error: storageBlocked ? recoverabilityText : state.error ?? (ledgerOutran ? (state.create ? createdText : appliedText) : null),
    deleteTarget,
    deleteConfirmText: state.deleteConfirmText,
    deleteConfirmed: deleteTarget !== null && state.deleteConfirmText === deleteTarget.collection,
    deletePending: state.deletePending,
    deleteError: state.deleteError,
    recovery,
    rename: (name) => { if (!editingBlocked) update(identity, (current) => renamed(current, name)); },
    editField: (index, edit) => { if (!editingBlocked) update(identity, (current) => redrafted(current.fields.map((field, position) => position === index ? applyEdit(field, edit, current.name) : field))); },
    addField: () => { if (!editingBlocked) update(identity, (current) => redrafted([...current.fields, newField()])); },
    removeField: (index) => { if (!editingBlocked) update(identity, (current) => current.fields[index]?.system ? {} : redrafted(current.fields.filter((_, position) => position !== index))); },
    setIndexes: (indexes) => { if (!editingBlocked) update(identity, { indexes, plan: null, authPlan: null, confirmed: false }); },
    setView: (view) => { if (!editingBlocked) update(identity, { view, plan: null, authPlan: null, confirmed: false }); },
    confirmDestructive: (confirmed) => update(identity, { confirmed }),
    confirmDeleteName: (deleteConfirmText) => update(identity, { deleteConfirmText }),
    requestPreview: () => void requestPreview(),
    requestApply: () => void requestApply(),
    requestDeletePlan: () => void requestDeletePlan(),
    cancelDelete: () => update(identity, { deletePlan: null, deleteConfirmText: "", deleteError: null }),
    requestDelete: () => void requestDelete(),
  };
}
