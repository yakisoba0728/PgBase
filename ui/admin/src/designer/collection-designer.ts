import { useCallback, useEffect, useMemo, useReducer } from "react";
import { useBackend } from "../auth/session";
import { parseJSON, stringifyJSON } from "../backend/json";
import {
  SchemaError,
  type CollectionCatalog,
  type CollectionChange,
  type CollectionDefinition,
  type FieldInput,
  type FieldType,
  type PlannedChange,
  type PlannedField,
  type RelationCardinality,
  type RelationDeleteAction,
} from "../backend/ports";
import { adminRouteIdentity, useRouteGuard, type AdminRoute } from "../routing/admin-location";

interface DraftRelation {
  targetCollection: string;
  cardinality: RelationCardinality;
  minSelect: string;
  maxSelect: string;
  onDelete: RelationDeleteAction;
}

export interface DraftField {
  name: string;
  type: FieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  hasDefault: boolean;
  defaultText: string;
  relation?: DraftRelation;
  // Whether the target was picked as "(self)". Re-deriving self-ness from
  // `targetCollection === name` would retarget a relation the moment a
  // half-typed name happens to equal an existing collection.
  selfRelation?: boolean;
}

export interface RelationTargetOption { name: string; label: string }

export type DraftEdit =
  | { set: "name"; value: string }
  | { set: "defaultText"; value: string }
  | { set: "target"; value: string }
  | { set: "minSelect"; value: string }
  | { set: "maxSelect"; value: string }
  | { set: "required"; value: boolean }
  | { set: "unique"; value: boolean }
  | { set: "indexed"; value: boolean }
  | { set: "hasDefault"; value: boolean }
  | { set: "type"; value: FieldType }
  | { set: "cardinality"; value: RelationCardinality }
  | { set: "onDelete"; value: RelationDeleteAction };

export type DesignerMode = { kind: "create" } | { kind: "edit"; collection: string };

class DraftError extends Error {}

const dataBlockedText = "Existing data blocks this change (for example missing or duplicate values).";
const catalogMovedText = "The catalog changed. Preview the collection again before applying.";

const newRelation = (): DraftRelation => ({ targetCollection: "", cardinality: "single", minSelect: "0", maxSelect: "1", onDelete: "restrict" });

const newField = (): DraftField => ({ name: "", type: "text", required: false, unique: false, indexed: false, hasDefault: false, defaultText: "" });

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

export function buildInput(operation: "create" | "update", name: string, fields: DraftField[]): CollectionChange {
  return {
    operation,
    name,
    fields: fields.map((field, index) => {
      const base: FieldInput = { name: field.name, type: field.type, required: field.required, unique: field.unique, indexed: field.indexed };
      if (field.type === "relation") return { ...base, relation: relationInput(field, index) };
      return field.hasDefault ? { ...base, default: parseDefault(field.type, field.defaultText) } : base;
    }),
  };
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
      name: field.name,
      type: field.type,
      required: field.required,
      unique: field.unique,
      indexed: field.indexed,
      hasDefault: field.default !== undefined,
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

function withType(field: DraftField, type: FieldType): DraftField {
  if (type !== "relation") return { ...field, type, relation: undefined, selfRelation: false };
  const relation = field.relation ?? newRelation();
  return {
    ...field,
    type,
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
  const relation = field.relation;
  if (edit.set === "name") return { ...field, name: edit.value };
  if (edit.set === "defaultText") return { ...field, defaultText: edit.value };
  if (edit.set === "hasDefault") return { ...field, hasDefault: edit.value };
  if (edit.set === "unique") return { ...field, unique: edit.value };
  if (edit.set === "indexed") return { ...field, indexed: edit.value };
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
  return error instanceof DraftError ? error.message : fallback;
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

interface State {
  identity: string;
  create: boolean;
  phase: "loading" | "missing" | "unavailable" | "ready";
  name: string;
  tableName: string;
  definition: CollectionDefinition | null;
  catalog: CollectionCatalog | null;
  targetsLoading: boolean;
  targetsFailed: boolean;
  fields: DraftField[];
  plan: PlannedChange | null;
  confirmed: boolean;
  pending: boolean;
  error: string | null;
  deletePlan: PlannedChange | null;
  deletePending: boolean;
  deleteError: string | null;
}

type Action =
  | { type: "reset"; identity: string; mode: DesignerMode }
  | { type: "targets-loading" }
  | { type: "loaded"; identity: string; catalog: CollectionCatalog }
  | { type: "load-failed"; identity: string }
  | { type: "rename"; name: string }
  | { type: "field"; index: number; edit: DraftEdit }
  | { type: "add-field" }
  | { type: "remove-field"; index: number }
  | { type: "confirm"; confirmed: boolean }
  | { type: "message"; message: string | null }
  | { type: "pending"; identity: string; pending: boolean }
  | { type: "previewed"; identity: string; plan: PlannedChange }
  | { type: "settle"; identity: string; message: string | null; clearPlan: boolean }
  | { type: "delete-pending"; identity: string; pending: boolean }
  | { type: "delete-planned"; identity: string; plan: PlannedChange | null }
  | { type: "delete-failed"; identity: string; message: string };

function initial(mode: DesignerMode, identity: string): State {
  const create = mode.kind === "create";
  return {
    identity,
    create,
    phase: create ? "ready" : "loading",
    name: create ? "" : mode.collection,
    tableName: "",
    definition: null,
    catalog: null,
    targetsLoading: false,
    targetsFailed: false,
    fields: create ? [newField()] : [],
    plan: null,
    confirmed: false,
    pending: false,
    error: null,
    deletePlan: null,
    deletePending: false,
    deleteError: null,
  };
}

// Editing the draft always drops the approved plan: a preview only describes
// the input it was produced from.
function redrafted(state: State, fields: DraftField[]): State {
  return { ...state, fields, plan: null, confirmed: false };
}

function reduce(state: State, action: Action): State {
  if (action.type === "reset") return initial(action.mode, action.identity);
  // A result that arrives after this designer moved to another collection can
  // no longer describe what is on screen.
  if ("identity" in action && action.identity !== state.identity) return state;
  switch (action.type) {
    case "targets-loading": return { ...state, targetsLoading: true };
    case "loaded": {
      const base = { ...state, catalog: action.catalog, targetsLoading: false, targetsFailed: false };
      if (state.create) return base;
      const definition = action.catalog.get(state.name);
      if (definition === undefined) return { ...base, phase: "missing" };
      return { ...base, phase: "ready", definition, name: definition.name, tableName: definition.tableName, fields: draftFields(definition, action.catalog) };
    }
    case "load-failed": return state.create
      ? { ...state, targetsLoading: false, targetsFailed: true }
      : { ...state, phase: "unavailable" };
    case "rename": return {
      ...state,
      name: action.name,
      plan: null,
      confirmed: false,
      fields: state.fields.map((field) => field.selfRelation === true && field.relation !== undefined
        ? { ...field, relation: { ...field.relation, targetCollection: action.name } }
        : field),
    };
    case "field": return redrafted(state, state.fields.map((field, index) => index === action.index ? applyEdit(field, action.edit, state.name) : field));
    case "add-field": return redrafted(state, [...state.fields, newField()]);
    case "remove-field": return redrafted(state, state.fields.filter((_, index) => index !== action.index));
    case "confirm": return { ...state, confirmed: action.confirmed };
    case "message": return { ...state, error: action.message };
    case "pending": return { ...state, pending: action.pending };
    case "previewed": return { ...state, plan: action.plan, confirmed: false };
    case "settle": return { ...state, error: action.message, plan: action.clearPlan ? null : state.plan, confirmed: action.clearPlan ? false : state.confirmed };
    case "delete-pending": return { ...state, deletePending: action.pending };
    case "delete-planned": return { ...state, deletePlan: action.plan, deleteError: null };
    case "delete-failed": return { ...state, deletePlan: null, deleteError: action.message };
  }
}

export interface CollectionDesigner {
  phase: State["phase"];
  create: boolean;
  name: string;
  tableName: string;
  fields: DraftField[];
  relationTargets: RelationTargetOption[];
  relationTargetsLoading: boolean;
  relationTargetsFailed: boolean;
  isNewField(name: string): boolean;
  preview: PlannedChange | null;
  removedFields: string[];
  destructiveConfirmed: boolean;
  applyBlocked: boolean;
  pending: boolean;
  error: string | null;
  deletePlan: PlannedChange | null;
  deletePending: boolean;
  deleteError: string | null;
  rename(name: string): void;
  editField(index: number, edit: DraftEdit): void;
  addField(): void;
  removeField(index: number): void;
  confirmDestructive(confirmed: boolean): void;
  requestPreview(): void;
  requestApply(): void;
  requestDeletePlan(): void;
  cancelDelete(): void;
  requestDelete(): void;
}

function applyFailure(caught: unknown, create: boolean): { message: string; clearPlan: boolean } {
  const reason = caught instanceof SchemaError ? caught.reason : "failed";
  if (reason === "same-version-conflict") {
    return {
      message: create
        ? "A collection with this name likely already exists."
        : "The plan conflicts with the current state. Preview again; if it keeps failing, existing data may be blocking it.",
      clearPlan: true,
    };
  }
  if (reason === "catalog-moved") return { message: catalogMovedText, clearPlan: true };
  if (!create && reason === "data-blocked") return { message: schemaMessage(caught, dataBlockedText, dataBlockedText), clearPlan: false };
  return { message: schemaMessage(caught, create ? "Unable to create the collection." : "Unable to apply the collection change."), clearPlan: false };
}

export function useCollectionDesigner(mode: DesignerMode, navigate: (route: AdminRoute) => void): CollectionDesigner {
  const { schema } = useBackend();
  const identity = adminRouteIdentity(mode.kind === "create" ? { kind: "collection-create" } : { kind: "collection-edit", collection: mode.collection });
  const active = useRouteGuard(identity);
  const [state, dispatch] = useReducer(reduce, mode, (initialMode) => initial(initialMode, identity));
  if (state.identity !== identity) dispatch({ type: "reset", identity, mode });

  const wantsCatalog = state.catalog === null && !state.targetsFailed && state.phase !== "missing" && state.phase !== "unavailable" &&
    (!state.create || state.fields.some((field) => field.type === "relation"));

  useEffect(() => {
    if (!wantsCatalog) return;
    const controller = new AbortController();
    dispatch({ type: "targets-loading" });
    void schema.catalog(controller.signal)
      .then((catalog) => { if (!controller.signal.aborted) dispatch({ type: "loaded", identity, catalog }); })
      .catch(() => { if (!controller.signal.aborted) dispatch({ type: "load-failed", identity }); });
    return () => controller.abort();
  }, [identity, schema, wantsCatalog]);

  const input = useMemo(() => {
    if (!state.create && state.definition === null) return null;
    try { return buildInput(state.create ? "create" : "update", state.name, state.fields); } catch { return null; }
  }, [state.create, state.definition, state.fields, state.name]);
  const preview = state.plan !== null && input !== null && state.plan.matches(input) ? state.plan : null;
  const removedFields = useMemo(() => {
    if (preview === null || state.definition === null) return [];
    const planned = new Set(preview.fields.map((field) => field.name));
    return state.definition.fields.filter((field) => !planned.has(field.name)).sort((left, right) => left.ordinal - right.ordinal).map((field) => field.name);
  }, [preview, state.definition]);
  const existing = useMemo(() => new Set(state.definition?.fields.map((field) => field.name) ?? []), [state.definition]);

  const requestPreview = useCallback(async () => {
    if (state.pending || (!state.create && state.definition === null)) return;
    dispatch({ type: "message", message: null });
    let change: CollectionChange;
    try { change = buildInput(state.create ? "create" : "update", state.name, state.fields); }
    catch (caught: unknown) { dispatch({ type: "message", message: draftMessage(caught, "Collection input is invalid.") }); return; }
    dispatch({ type: "pending", identity, pending: true });
    try {
      dispatch({ type: "previewed", identity, plan: await schema.plan(change) });
    } catch (caught: unknown) {
      const fallback = state.create ? "Unable to preview the collection." : "Unable to preview the collection change.";
      dispatch({ type: "settle", identity, message: schemaMessage(caught, fallback), clearPlan: true });
    } finally {
      dispatch({ type: "pending", identity, pending: false });
    }
  }, [identity, schema, state.create, state.definition, state.fields, state.name, state.pending]);

  const requestApply = useCallback(async () => {
    if (preview === null || state.pending || (removedFields.length > 0 && !state.confirmed)) return;
    dispatch({ type: "pending", identity, pending: true });
    dispatch({ type: "message", message: null });
    try {
      await schema.apply(preview);
      if (!active()) return;
      navigate(state.create ? { kind: "collections" } : { kind: "records", collection: state.definition?.name ?? state.name });
    } catch (caught: unknown) {
      dispatch({ type: "settle", identity, ...applyFailure(caught, state.create) });
    } finally {
      dispatch({ type: "pending", identity, pending: false });
    }
  }, [active, identity, navigate, preview, removedFields.length, schema, state.confirmed, state.create, state.definition, state.name, state.pending]);

  const requestDeletePlan = useCallback(async () => {
    if (state.definition === null || state.deletePending) return;
    dispatch({ type: "delete-pending", identity, pending: true });
    dispatch({ type: "delete-planned", identity, plan: null });
    try {
      dispatch({ type: "delete-planned", identity, plan: await schema.plan({ operation: "delete", name: state.definition.name }) });
    } catch (caught: unknown) {
      dispatch({ type: "delete-failed", identity, message: schemaMessage(caught, "Unable to preview the collection deletion.") });
    } finally {
      dispatch({ type: "delete-pending", identity, pending: false });
    }
  }, [identity, schema, state.definition, state.deletePending]);

  const requestDelete = useCallback(async () => {
    if (state.deletePlan === null || state.deletePending) return;
    dispatch({ type: "delete-pending", identity, pending: true });
    try {
      await schema.apply(state.deletePlan);
      if (!active()) return;
      navigate({ kind: "collections" });
    } catch (caught: unknown) {
      const reason = caught instanceof SchemaError ? caught.reason : "failed";
      dispatch({
        type: "delete-failed",
        identity,
        message: reason === "referenced" ? schemaMessage(caught, "Unable to delete the collection.", "Collection is referenced.")
          : reason === "catalog-moved" ? "The catalog changed. Preview the deletion again before applying."
            : schemaMessage(caught, "Unable to delete the collection."),
      });
    } finally {
      dispatch({ type: "delete-pending", identity, pending: false });
    }
  }, [active, identity, navigate, schema, state.deletePending, state.deletePlan]);

  return {
    phase: state.phase,
    create: state.create,
    name: state.name,
    tableName: state.tableName,
    fields: state.fields,
    relationTargets: relationTargets(state.catalog, state.name),
    relationTargetsLoading: state.targetsLoading,
    relationTargetsFailed: state.targetsFailed,
    isNewField: (name) => !state.create && !existing.has(name),
    preview,
    removedFields,
    destructiveConfirmed: state.confirmed,
    applyBlocked: removedFields.length > 0 && !state.confirmed,
    pending: state.pending,
    error: state.error,
    deletePlan: state.deletePlan,
    deletePending: state.deletePending,
    deleteError: state.deleteError,
    rename: (name) => dispatch({ type: "rename", name }),
    editField: (index, edit) => dispatch({ type: "field", index, edit }),
    addField: () => dispatch({ type: "add-field" }),
    removeField: (index) => dispatch({ type: "remove-field", index }),
    confirmDestructive: (confirmed) => dispatch({ type: "confirm", confirmed }),
    requestPreview: () => void requestPreview(),
    requestApply: () => void requestApply(),
    requestDeletePlan: () => void requestDeletePlan(),
    cancelDelete: () => dispatch({ type: "delete-planned", identity, plan: null }),
    requestDelete: () => void requestDelete(),
  };
}
