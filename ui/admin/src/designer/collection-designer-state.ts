import {
  CollectionCatalog,
  type AuthCollectionScaffold,
  type CollectionChange,
  type CollectionDefinition,
  type CollectionType,
  type CompoundIndex,
  type PlannedChange,
  type PlannedField,
  type RecordIDPolicy,
  type SchemaDocument,
  type SchemaDocumentCollection,
  type SchemaImportPlan,
  type ViewDefinition,
} from "../backend/ports";
import {
  DraftError,
  buildInput,
  draftFields,
  draftSignature,
  newField,
  scaffoldFields,
  type DesignerMode,
  type DraftField,
} from "./collection-draft";
import {
  PendingSchemaOperationStore,
  type UndecidedSchemaOperation,
} from "./pending-schema-operation-store";

export interface State {
  identity: string;
  create: boolean;
  phase: "loading" | "missing" | "unavailable" | "ready";
  name: string;
  tableName: string;
  definition: CollectionDefinition | null;
  catalog: CollectionCatalog | null;
  collectionType?: CollectionType;
  recordId?: RecordIDPolicy;
  advanced?: import("../backend/ports").CollectionAdvancedOptions;
  authBlueprint?: AuthCollectionScaffold;
  duplicateSource?: string;
  scaffold?: "base" | "auth" | "view";
  targetsLoading: boolean;
  targetsFailed: boolean;
  fields: DraftField[];
  indexes?: CompoundIndex[];
  view?: ViewDefinition;
  plan: PlannedChange | null;
  authPlan: AuthImportPlan | null;
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
export interface CollectionPreview {
  baseVersion: number;
  fields: readonly PlannedField[];
  sql: readonly string[];
}
export interface AuthImportPlan extends CollectionPreview {
  plan: SchemaImportPlan;
  signature: string;
}
type UndecidedApply = UndecidedSchemaOperation;
export const recoverabilityText =
  "Unable to use browser storage safely for this operation. Restore it and reload before applying.";
export const pendingStore = new PendingSchemaOperationStore();
export function initial(mode: DesignerMode, identity: string): State {
  const create = mode.kind !== "edit";
  const name = create ? "" : mode.collection;
  const fields = create ? [newField()] : [];
  return {
    identity,
    create,
    phase: mode.kind === "create" ? "ready" : "loading",
    name,
    tableName: "",
    definition: null,
    catalog: null,
    targetsLoading: false,
    targetsFailed: false,
    fields,
    indexes: create ? [] : undefined,
    ...(mode.kind === "duplicate" ? { duplicateSource: mode.collection } : {}),
    ...(mode.kind === "scaffold" ? { scaffold: mode.scaffold } : {}),
    plan: null,
    authPlan: null,
    confirmed: false,
    pending: false,
    error: null,
    deletePlan: null,
    deleteConfirmText: "",
    deletePending: false,
    deleteError: null,
    collectionType: mode.kind === "scaffold" ? mode.scaffold : undefined,
    pristine: draftSignature(
      name,
      fields,
      create ? [] : undefined,
      undefined,
      mode.kind === "scaffold" ? mode.scaffold : undefined,
    ),
    undecided: null,
    terminal: false,
  };
}

export const redrafted = (fields: DraftField[]): Partial<State> => ({
  fields,
  plan: null,
  authPlan: null,
  confirmed: false,
});

// Both the visible preview and the command sent to the server must describe
// the same draft, including its view mode and immutable identity policy.
export function collectionChange(state: State): CollectionChange {
  return { ...buildInput(
    state.create ? "create" : "update",
    state.name,
    state.fields,
    state.indexes,
    state.view,
    state.collectionType,
    state.recordId,
  ), ...state.advanced };
}

function editableView(definition: CollectionDefinition, catalog: CollectionCatalog): ViewDefinition | undefined {
  const view = definition.view;
  if (view === undefined) return undefined;
  if (typeof view.sql === "string") return { sql: view.sql };
  return {
    source: view.sourceCollectionId === undefined ? view.source : catalog.byID(view.sourceCollectionId)?.name ?? view.source,
    projection: [...view.projection],
    ...(view.filter === undefined ? {} : { filter: view.filter }),
  };
}

export function loadedState(
  current: State,
  catalog: CollectionCatalog,
  scaffolds?: {
    base: SchemaDocumentCollection;
    auth: AuthCollectionScaffold;
    view: SchemaDocumentCollection;
  },
  document?: SchemaDocument,
): Partial<State> {
  const base = { catalog, targetsLoading: false, targetsFailed: false };
  if (current.create) {
    const source =
      current.duplicateSource === undefined
        ? undefined
        : catalog.get(current.duplicateSource);
    if (current.duplicateSource !== undefined && source === undefined)
      return { ...base, phase: "missing" };
    const requested =
      current.scaffold === undefined
        ? undefined
        : scaffolds?.[current.scaffold];
    if (current.scaffold !== undefined && requested === undefined)
      return { ...base, phase: "unavailable" };
    // A plain create only loads this catalog to resolve a relation target. It
    // has no template to hydrate, so keep the editable draft intact.
    if (source === undefined && current.scaffold === undefined) return base;
    const auth = current.scaffold === "auth" || source?.type === "auth";
    if (!auth && source !== undefined) {
      const fields = draftFields(source, catalog),
        indexes = (source.indexes ?? []).map((index) => ({
          name: index.name,
          fields: [...index.fields],
          unique: index.unique,
        })),
        view = editableView(source, catalog),
        name = `${source.name}_copy`,
        collectionType = source.type ?? (view === undefined ? "base" : "view");
      return {
        ...base,
        phase: "ready",
        name,
        fields,
        indexes,
        view,
        collectionType,
        recordId: source.recordId,
        advanced: source.validationMode ? { validationMode: source.validationMode } : undefined,
        pristine: draftSignature(name, fields, indexes, view, collectionType, source.recordId, source.validationMode ? { validationMode: source.validationMode } : undefined),
      };
    }
    const template = auth
      ? current.scaffold === "auth"
        ? scaffolds?.auth.collection
        : document?.collections.find((item) => item.name === source?.name)
      : (requested as SchemaDocumentCollection | undefined);
    const authBlueprint = auth
      ? current.scaffold === "auth"
        ? scaffolds?.auth
        : (() => {
            const authCollection = document?.authCollections?.find(
              (item) => item.name === source?.name,
            );
            return template === undefined || authCollection === undefined
              ? undefined
              : { collection: template, authCollection };
          })()
      : undefined;
    if (template === undefined || (auth && authBlueprint === undefined))
      return { ...base, phase: "unavailable" };
    const fields = scaffoldFields(template.fields),
      indexes = (template.indexes ?? []).map((index) => ({
        name: index.name,
        fields: [...index.fields],
        unique: index.unique,
      })),
      view = template.view,
      name = source === undefined ? "" : `${source.name}_copy`,
      collectionType = template.type ?? (view === undefined ? "base" : "view");
    return {
      ...base,
      phase: "ready",
      name,
      fields,
      indexes,
      view,
      collectionType,
      recordId: template.recordId,
      advanced: template.validationMode ? { validationMode: template.validationMode } : undefined,
      authBlueprint,
      pristine: draftSignature(name, fields, indexes, view, collectionType, template.recordId, template.validationMode ? { validationMode: template.validationMode } : undefined),
    };
  }
  const definition = catalog.get(current.name);
  if (definition === undefined) return { ...base, phase: "missing" };
  const fields = draftFields(definition, catalog);
  const view = editableView(definition, catalog);
  return {
    ...base,
    phase: "ready",
    definition,
    name: definition.name,
    tableName: definition.tableName,
    fields,
    indexes: undefined,
    view,
    collectionType: definition.type,
    recordId: definition.recordId,
    advanced: definition.validationMode !== undefined || definition.compatibilityName !== undefined ? { validationMode: definition.validationMode, compatibilityName: definition.compatibilityName } : undefined,
    pristine: draftSignature(
      definition.name,
      fields,
      undefined,
      view,
      definition.type,
      definition.recordId,
      definition.validationMode !== undefined || definition.compatibilityName !== undefined ? { validationMode: definition.validationMode, compatibilityName: definition.compatibilityName } : undefined,
    ),
  };
}

export function authImportDocument(state: State): SchemaDocument {
  if (state.authBlueprint === undefined)
    throw new DraftError("The authentication template is unavailable.");
  const change = buildInput(
    "create",
    state.name,
    state.fields,
    state.indexes,
    undefined,
    "auth",
    state.recordId,
  );
  if (!("fields" in change) || change.fields === undefined)
    throw new DraftError("Authentication fields are unavailable.");
  const collection = {
    ...state.authBlueprint.collection,
    ...state.advanced,
    name: state.name,
    type: "auth" as const,
    fields: change.fields,
    recordId: state.recordId,
    ...(change.indexes === undefined ? {} : { indexes: change.indexes }),
  };
  const authCollection = {
    ...state.authBlueprint.authCollection,
    name: state.name,
    recordId: state.recordId,
    // The auth authority stores custom fields only; its system fields remain
    // in the catalog collection and are joined by userauth at runtime.
    fields: change.fields.filter((field) => field.system !== true),
  };
  return {
    format: "pgbase.schema/v1",
    collections: [collection],
    authCollections: [authCollection],
  };
}

export function renamed(current: State, name: string): Partial<State> {
  return {
    name,
    plan: null,
    authPlan: null,
    confirmed: false,
    fields: current.fields.map((field) =>
      field.selfRelation === true && field.relation !== undefined
        ? { ...field, relation: { ...field.relation, targetCollection: name } }
        : field,
    ),
  };
}

export interface DeleteTarget {
  collection: string;
  tableName: string;
  baseVersion: number;
  sql: readonly string[];
}

export function deleteTargetOf(
  definition: CollectionDefinition | null,
  plan: PlannedChange | null,
): DeleteTarget | null {
  if (definition === null || plan === null) return null;
  if (plan.operation !== "delete" || plan.collection !== definition.name)
    return null;
  return {
    collection: definition.name,
    tableName: definition.tableName,
    baseVersion: plan.baseVersion,
    sql: plan.sql,
  };
}
