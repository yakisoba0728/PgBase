import { useCallback, useEffect, useMemo, useRef } from "react";
import { useBackend } from "../auth/session";
import { parseJSON, stringifyJSON } from "../backend/json";
import {
  ApplyAttempt,
  CollectionCatalog,
  SchemaError,
  type CollectionChange,
  type CollectionDefinition,
  type FieldInput,
  type FieldType,
  type PlannedChange,
  type PlannedField,
  type RelationCardinality,
  type RelationDeleteAction,
} from "../backend/ports";
import { operationFailureMessage, useOperationRecovery, type OperationRecovery } from "../operations/operation-recovery";
import { adminRouteIdentity, useResourceState, useRouteGuard, type AdminRoute } from "../routing/admin-location";
import { useUnsavedDraft } from "../routing/unsaved-draft";

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
const catalogMovedText = "The catalog changed. Reload this page before making another schema change.";
const appliedText = "The change was applied. The edits you made after sending it are not saved yet — leave and reopen the collection before applying them.";
const createdText = "The collection was created. The edits you made after sending it are not part of it and are not saved — open the collection from the list to apply them.";

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

// The draft as it stood when the server last agreed with it. Comparing the
// whole draft is what makes an untouched form leave without a question.
const draftSignature = (name: string, fields: DraftField[]): string => stringifyJSON({ name, fields });

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
  deleteConfirmText: string;
  deletePending: boolean;
  deleteError: string | null;
  pristine: string;
  undecided: UndecidedApply | null;
  terminal: boolean;
}

type UndecidedApply = { kind: "change"; operationId: string; draft: string }
  | { kind: "drop"; operationId: string; draft: string };

function initial(mode: DesignerMode, identity: string): State {
  const create = mode.kind === "create";
  const name = create ? "" : mode.collection;
  const fields = create ? [newField()] : [];
  return {
    identity, create, phase: create ? "ready" : "loading", name, tableName: "",
    definition: null, catalog: null, targetsLoading: false, targetsFailed: false, fields,
    plan: null, confirmed: false, pending: false, error: null,
    deletePlan: null, deleteConfirmText: "", deletePending: false, deleteError: null,
    pristine: draftSignature(name, fields), undecided: null, terminal: false,
  };
}

const redrafted = (fields: DraftField[]): Partial<State> => ({ fields, plan: null, confirmed: false });

function loadedState(current: State, catalog: CollectionCatalog): Partial<State> {
  const base = { catalog, targetsLoading: false, targetsFailed: false };
  if (current.create) return base;
  const definition = catalog.get(current.name);
  if (definition === undefined) return { ...base, phase: "missing" };
  const fields = draftFields(definition, catalog);
  return {
    ...base, phase: "ready", definition, name: definition.name, tableName: definition.tableName, fields,
    pristine: draftSignature(definition.name, fields),
  };
}

function renamed(current: State, name: string): Partial<State> {
  return {
    name, plan: null, confirmed: false,
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
  editingBlocked: boolean;
  pending: boolean;
  error: string | null;
  deleteTarget: DeleteTarget | null;
  deleteConfirmText: string;
  deleteConfirmed: boolean;
  deletePending: boolean;
  deleteError: string | null;
  // An unsettled ledger outcome is neither applied nor failed.
  recovery: OperationRecovery | null;
  rename(name: string): void;
  editField(index: number, edit: DraftEdit): void;
  addField(): void;
  removeField(index: number): void;
  confirmDestructive(confirmed: boolean): void;
  confirmDeleteName(typed: string): void;
  requestPreview(): void;
  requestApply(): void;
  requestDeletePlan(): void;
  cancelDelete(): void;
  requestDelete(): void;
}

const operationFailed = (caught: unknown, fallback: string): string =>
  operationFailureMessage(caught instanceof SchemaError ? caught.details.operation?.[0] : undefined, fallback);

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
  const identity = adminRouteIdentity(mode.kind === "create" ? { kind: "collection-create" } : { kind: "collection-edit", collection: mode.collection });
  const active = useRouteGuard(identity);
  const [state, update] = useResourceState(identity, (current) => initial(mode, current));
  // Retries reuse one identity per approved plan; a fresh preview does not.
  const attempt = useRef(new ApplyAttempt());
  const recovery = useOperationRecovery(state.undecided?.operationId ?? null);
  const signature = draftSignature(state.name, state.fields);
  const live = useRef(signature);
  live.current = signature;
  const recovered = recovery?.status === "committed" ? state.undecided : null;
  const recoveryFailed = recovery?.status === "failed" && state.undecided !== null;
  const editingBlocked = state.terminal || (state.undecided !== null && recovery?.status !== "failed");
  const ledgerOutran = recovered?.kind === "change" && recovered.draft !== signature;
  let unsaved = signature !== state.pristine;
  if (state.undecided?.kind === "change" && recovery !== null) {
    if (recovery.status === "committed") unsaved = signature !== state.undecided.draft;
    else if (recovery.undecided) unsaved = true;
  }
  const savedDraft = useUnsavedDraft(identity, unsaved);

  const wantsCatalog = state.catalog === null && !state.targetsFailed && state.phase !== "missing" && state.phase !== "unavailable" &&
    (!state.create || state.fields.some((field) => field.type === "relation"));

  useEffect(() => {
    if (!wantsCatalog) return;
    const controller = new AbortController();
    update(identity, { targetsLoading: true });
    void schema.catalog(controller.signal)
      .then((catalog) => { if (!controller.signal.aborted) update(identity, (current) => loadedState(current, catalog)); })
      .catch(() => {
        if (controller.signal.aborted) return;
        update(identity, (current) => current.create ? { targetsLoading: false, targetsFailed: true } : { phase: "unavailable" });
      });
    return () => controller.abort();
  }, [identity, schema, update, wantsCatalog]);

  useEffect(() => {
    if (recovered === null) return;
    if (signature === recovered.draft) savedDraft();
    navigate(recovered.kind === "drop" || state.create
      ? { kind: "collections" }
      : { kind: "records", collection: state.definition?.name ?? state.name });
  }, [navigate, recovered, savedDraft, signature, state.create, state.definition?.name, state.name]);

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
    if (editingBlocked || state.pending || (!state.create && state.definition === null)) return;
    let change: CollectionChange;
    try { change = buildInput(state.create ? "create" : "update", state.name, state.fields); }
    catch (caught: unknown) { update(identity, { error: draftMessage(caught, "Collection input is invalid.") }); return; }
    update(identity, { error: null, pending: true });
    try {
      const planned = await schema.plan(change);
      if (!state.create && planned.baseVersion !== state.catalog?.version) {
        update(identity, { error: catalogMovedText, plan: null, confirmed: false });
        return;
      }
      update(identity, { plan: planned, confirmed: false, deletePlan: null, deleteConfirmText: "", ...(recoveryFailed ? { undecided: null } : {}) });
    } catch (caught: unknown) {
      const fallback = state.create ? "Unable to preview the collection." : "Unable to preview the collection change.";
      update(identity, { error: schemaMessage(caught, fallback), plan: null, confirmed: false });
    } finally {
      update(identity, { pending: false });
    }
  }, [editingBlocked, identity, recoveryFailed, schema, state.create, state.definition, state.fields, state.name, state.pending, update]);

  const requestApply = useCallback(async () => {
    if (editingBlocked || recoveryFailed || preview === null || state.pending || (removedFields.length > 0 && !state.confirmed)) return;
    update(identity, { pending: true, error: null, undecided: null });
    try {
      const outcome = await schema.apply(preview, attempt.current);
      if (!active()) return;
      // Stay until the ledger decides an ambiguous outcome.
      if (outcome.kind === "undecided") { update(identity, { undecided: { kind: "change", operationId: outcome.operationId, draft: signature } }); return; }
      // Rebase edit warnings on the collection produced by the sent draft.
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
      // Keep an outrunning draft only when the response supplies its new baseline;
      // otherwise navigate through the departure guard rather than edit stale data.
      if (outran && !state.create && outcome.definition !== undefined) return;
      if (!outran) savedDraft();
      navigate(state.create ? { kind: "collections" } : { kind: "records", collection: state.definition?.name ?? state.name });
    } catch (caught: unknown) {
      update(identity, applyFailure(caught, state.create));
    } finally {
      update(identity, { pending: false });
    }
  }, [active, editingBlocked, identity, navigate, preview, recoveryFailed, removedFields.length, savedDraft, schema, signature, state.confirmed, state.create, state.definition, state.name, state.pending, update]);

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
    try {
      const outcome = await schema.apply(state.deletePlan, attempt.current);
      if (!active()) return;
      if (outcome.kind === "undecided") { update(identity, { undecided: { kind: "drop", operationId: outcome.operationId, draft: signature } }); return; }
      update(identity, { terminal: true, deletePlan: null, deleteConfirmText: "" });
      if (live.current === signature) savedDraft();
      navigate({ kind: "collections" });
    } catch (caught: unknown) {
      update(identity, deleteFailure(caught));
    } finally {
      update(identity, { deletePending: false });
    }
  }, [active, deleteTarget, editingBlocked, identity, navigate, recoveryFailed, savedDraft, schema, signature, state.deleteConfirmText, state.deletePending, state.deletePlan, update]);

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
    applyBlocked: editingBlocked || recoveryFailed || (removedFields.length > 0 && !state.confirmed),
    editingBlocked,
    pending: state.pending,
    error: state.error ?? (ledgerOutran ? (state.create ? createdText : appliedText) : null),
    deleteTarget,
    deleteConfirmText: state.deleteConfirmText,
    deleteConfirmed: deleteTarget !== null && state.deleteConfirmText === deleteTarget.collection,
    deletePending: state.deletePending,
    deleteError: state.deleteError,
    recovery,
    rename: (name) => { if (!editingBlocked) update(identity, (current) => renamed(current, name)); },
    editField: (index, edit) => { if (!editingBlocked) update(identity, (current) => redrafted(current.fields.map((field, position) => position === index ? applyEdit(field, edit, current.name) : field))); },
    addField: () => { if (!editingBlocked) update(identity, (current) => redrafted([...current.fields, newField()])); },
    removeField: (index) => { if (!editingBlocked) update(identity, (current) => redrafted(current.fields.filter((_, position) => position !== index))); },
    confirmDestructive: (confirmed) => update(identity, { confirmed }),
    confirmDeleteName: (deleteConfirmText) => update(identity, { deleteConfirmText }),
    requestPreview: () => void requestPreview(),
    requestApply: () => void requestApply(),
    requestDeletePlan: () => void requestDeletePlan(),
    cancelDelete: () => update(identity, { deletePlan: null, deleteConfirmText: "", deleteError: null }),
    requestDelete: () => void requestDelete(),
  };
}
