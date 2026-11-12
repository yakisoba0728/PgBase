import { useCallback, useEffect, useMemo, useRef } from "react";
import { useBackend, useSession } from "../auth/session";
import {
  CreateAttempt,
  RecordError,
  type CollectionCatalog,
  type CollectionDefinition,
  type FieldDefinition,
  type RecordPage,
  type RecordIDPolicy,
  type VersionedRecord,
} from "../backend/ports";
import { adminRouteIdentity, useResourceState, useRouteGuard, type AdminRoute } from "../routing/admin-location";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import { acceptedDraft,
  blankState,
  dirtyNames,
  encodeDraft,
  initialDraft,
  validationText,
  type Draft,
  type FieldState,
} from "./record-codec";
import { clearDraft, downloadJSON, readDraft, writeDraft, type RecordScope } from "./record-productivity";

const staleText = "This record changed after you loaded it. Reload the latest version before saving or deleting.";
const missingText = "This record no longer exists.";
const savedText = "The record was saved. The edits you made after sending it are not saved yet — save again to store them, or leave this page to discard them.";
const createdText = "The record was created. The edits you made after sending it are not part of it — saving again creates another record.";
const alwaysSortable = new Set(["id", "created_at"]);

function ordered(collection: CollectionDefinition | null): FieldDefinition[] {
  return collection === null ? [] : collection.fields.slice().sort((left, right) => left.ordinal - right.ordinal);
}

// An explicit ID participates in draft recovery and unsaved-change protection,
// but is offered only while creating records in a text-identity collection.
const recordIDField: FieldDefinition = { name: "id", type: "text", required: false, unique: false, indexed: false, ordinal: -1 };
function draftFields(collection: CollectionDefinition, isCreate: boolean): FieldDefinition[] {
  return isCreate && collection.recordId?.kind === "text" ? [recordIDField, ...collection.fields] : collection.fields;
}

function relationPolicies(catalog: CollectionCatalog | null): ReadonlyMap<string, RecordIDPolicy> {
  const policies = new Map<string, RecordIDPolicy>();
  for (const collection of catalog?.all() ?? []) {
    if (collection.id !== undefined) policies.set(collection.id, collection.recordId ?? { kind: "uuid" });
  }
  return policies;
}

function rejected(caught: unknown): boolean {
  return caught instanceof RecordError && caught.reason === "rejected";
}

interface ListState {
  identity: string;
  collection: CollectionDefinition | null;
  page: RecordPage | null;
  failed: boolean;
  filterInvalid: boolean;
  filterText: string;
  appliedFilter: string;
  queryOptions: import("../backend/ports").RecordQueryOptions;
  sort: string;
  // The cursor that opened every page after the first, oldest first. Empty
  // means the first page, so its length is also how far the chain has walked.
  cursors: string[];
  selected: Set<string>;
  actionMessage: string | null;
  actionPending: boolean;
}

function initialList(identity: string): ListState {
  return { identity, collection: null, page: null, failed: false, filterInvalid: false, filterText: "", appliedFilter: "", queryOptions: {}, sort: "", cursors: [], selected: new Set(), actionMessage: null, actionPending: false };
}

function nextSort(current: string, column: string): string {
  const descending = current.startsWith("-");
  if ((descending ? current.slice(1) : current) !== column) return column;
  return descending ? "" : `-${column}`;
}

export interface RecordList {
  collection: CollectionDefinition | null;
  columns: string[];
  page: RecordPage | null;
  failed: boolean;
  filterInvalid: boolean;
  filterText: string;
  filtered: boolean;
  filterApplied: boolean;
  hasPrevious: boolean;
  fieldFor(column: string): FieldDefinition | undefined;
  sortDirection(column: string): "ascending" | "descending" | undefined;
  sortable(column: string): boolean;
  queryOptions: import("../backend/ports").RecordQueryOptions;
  setQueryOptions(value: import("../backend/ports").RecordQueryOptions): void;
  setFilterText(text: string): void;
  applyFilter(): void;
  clear(): void;
  toggleSort(column: string): void;
  previousPage(): void;
  nextPage(): void;
  selected: ReadonlySet<string>;
  actionMessage: string | null;
  actionPending: boolean;
  select(id: string, selected: boolean): void;
  selectMany(ids: string[], selected: boolean): void;
  deleteSelected(): void;
  exportJSON(): void;
}

export function useRecordList(collectionName: string): RecordList {
  const { records, schema } = useBackend();
  const identity = adminRouteIdentity({ kind: "records", collection: collectionName });
  const [state, update] = useResourceState(identity, initialList);
  const inFlight = useRef<AbortController | null>(null);
  const actionFlight = useRef<AbortController | null>(null);
  const active = useRouteGuard(identity);
  useEffect(() => () => actionFlight.current?.abort(), []);

  useEffect(() => {
    const controller = new AbortController();
    void schema.catalog(controller.signal).then((catalog) => {
      const definition = catalog.get(collectionName);
      update(identity, definition === undefined ? { failed: true } : { collection: definition });
    }).catch(() => { if (!controller.signal.aborted) update(identity, { failed: true }); });
    return () => controller.abort();
  }, [collectionName, identity, schema, update]);

  // Both directions read the same query under the same chain, so stepping back
  // re-reads the page the server issued that cursor for. A continuation fetched
  // under older filter or sort values must not overwrite the page the current
  // ones produced, so the previous request is abandoned first.
  const openPage = useCallback(async (cursors: string[]) => {
    inFlight.current?.abort();
    const controller = new AbortController();
    inFlight.current = controller;
    try {
      const page = await records.list(collectionName, { ...state.queryOptions, cursor: cursors.at(-1), filter: state.appliedFilter, sort: state.sort }, controller.signal);
      if (!controller.signal.aborted) update(identity, { page, cursors, filterInvalid: false, failed: false });
    } catch (caught: unknown) {
      if (!controller.signal.aborted) update(identity, rejected(caught) ? { filterInvalid: true } : { failed: true });
    }
  }, [collectionName, identity, records, state.appliedFilter, state.sort, state.queryOptions, update]);

  // A new query has no chain to walk: its cursors address the old query's pages.
  useEffect(() => {
    update(identity, { page: null, cursors: [] });
    void openPage([]);
    return () => inFlight.current?.abort();
  }, [identity, openPage, update]);

  const fields = state.collection?.fields;
  return {
    queryOptions: state.queryOptions,
    setQueryOptions: (queryOptions) => update(identity, { queryOptions, selected: new Set(), actionMessage: null }),
    collection: state.collection,
    columns: state.collection === null ? [] : ["id", "created_at", "updated_at", ...ordered(state.collection).map((field) => field.name)],
    page: state.page,
    failed: state.failed,
    filterInvalid: state.filterInvalid,
    filterText: state.filterText,
    filtered: state.appliedFilter !== "" || state.sort !== "",
    filterApplied: state.appliedFilter !== "",
    hasPrevious: state.cursors.length > 0,
    fieldFor: (column) => fields?.find((field) => field.name === column),
    sortDirection: (column) => state.sort === column ? "ascending" : state.sort === `-${column}` ? "descending" : undefined,
    sortable: (column) => alwaysSortable.has(column) || (fields?.some((field) =>
      (field.type === "geoPoint" ? column === `${field.name}.lon` || column === `${field.name}.lat` : field.name === column) &&
      !["jsonb", "password", "file"].includes(field.type) &&
      (field.type !== "relation" || field.relation?.cardinality === "single") &&
      (field.indexed || field.unique),
    ) ?? false),
    setFilterText: (filterText) => update(identity, { filterText }),
    applyFilter: () => update(identity, (current) => ({ appliedFilter: current.filterText.trim() })),
    clear: () => update(identity, { filterText: "", appliedFilter: "", queryOptions: {}, sort: "" }),
    toggleSort: (column) => update(identity, (current) => ({ sort: nextSort(current.sort, column) })),
    previousPage: () => { if (state.cursors.length > 0) void openPage(state.cursors.slice(0, -1)); },
    nextPage: () => { if (state.page?.nextCursor !== undefined) void openPage([...state.cursors, state.page.nextCursor]); },
    selected: state.selected,
    actionMessage: state.actionMessage,
    actionPending: state.actionPending,
    select: (id, selected) => update(identity, (current) => {
      const next = new Set(current.selected); selected ? next.add(id) : next.delete(id); return { selected: next, actionMessage: null };
    }),
    selectMany: (ids, selected) => update(identity, (current) => {
      const next = new Set(current.selected); for (const id of ids) selected ? next.add(id) : next.delete(id); return { selected: next, actionMessage: null };
    }),
    deleteSelected: () => {
      const ids = [...state.selected];
      if (ids.length === 0 || state.actionPending || !window.confirm(`Delete ${ids.length} selected record${ids.length === 1 ? "" : "s"}?`)) return;
      update(identity, { actionPending: true, actionMessage: null });
      actionFlight.current?.abort();
      const controller = new AbortController(); actionFlight.current = controller;
      void Promise.allSettled(ids.map(async (id) => records.remove(await records.get(collectionName, id, controller.signal))))
        .then((outcomes) => {
          if (!active()) return;
          const failed = ids.filter((_, index) => outcomes[index].status === "rejected");
          update(identity, { selected: new Set(failed), actionMessage: failed.length === 0
            ? `Deleted ${ids.length} record${ids.length === 1 ? "" : "s"}.`
            : `Deleted ${ids.length - failed.length} of ${ids.length} records. ${failed.length} failed and remain selected.` });
          void openPage(state.cursors);
        }).finally(() => { if (active()) update(identity, { actionPending: false }); });
    },
    exportJSON: () => {
      if (state.actionPending) return;
      const selected = new Set(state.selected);
      update(identity, { actionPending: true, actionMessage: null });
      actionFlight.current?.abort();
      const controller = new AbortController(); actionFlight.current = controller;
      void (async () => {
        const items = [];
        let cursor: string | undefined;
        let pages = 0;
        do {
          const page = await records.list(collectionName, { ...state.queryOptions, cursor, filter: state.appliedFilter, sort: state.sort }, controller.signal);
          items.push(...page.items.filter((item) => selected.size === 0 || selected.has(String(item.id))));
          cursor = page.nextCursor; pages += 1;
        } while (cursor !== undefined && pages < 100);
        if (cursor !== undefined) throw new Error("export page limit");
        if (!active()) return;
        downloadJSON(`${collectionName}-${selected.size > 0 ? "selected" : "filtered"}.json`, items);
        update(identity, { actionMessage: `Exported ${items.length} ${selected.size > 0 ? "selected" : "filtered"} record${items.length === 1 ? "" : "s"}.` });
      })().catch(() => { if (active()) update(identity, { actionMessage: "Unable to export every matching record within the 100-page limit." }); })
        .finally(() => { if (active()) update(identity, { actionPending: false }); });
    },
  };
}

interface EditorState {
  identity: string;
  loadedResource: string | null;
  collection: CollectionDefinition | null;
  catalog: CollectionCatalog | null;
  draft: Draft;
  initial: Draft;
  errors: Record<string, string>;
  message: string | null;
  pending: boolean;
  loaded: VersionedRecord | null;
  stale: boolean;
  reloads: number;
}

function initialEditor(identity: string): EditorState {
  return {
    identity, loadedResource: null, collection: null, catalog: null, draft: {}, initial: {},
    errors: {}, message: null, pending: false, loaded: null, stale: false, reloads: 0,
  };
}

export interface RecordEditor {
  isCreate: boolean;
  collection: CollectionDefinition | null;
  fields: FieldDefinition[];
  errors: Record<string, string>;
  message: string | null;
  pending: boolean;
  stale: boolean;
  recordID: string;
  setRecordID(value: string): void;
  targetOf(field: FieldDefinition): CollectionDefinition | undefined;
  stateOf(field: FieldDefinition): FieldState;
  setKind(field: FieldDefinition, kind: FieldState["kind"]): void;
  setValue(field: FieldDefinition, value: string | boolean): void;
  reload(): void;
	save(files?: Record<string, File[]>): void;
  remove(): void;
  duplicate(): void;
}

function saveFailure(caught: unknown, collection: CollectionDefinition, catalog: CollectionCatalog | null): Partial<EditorState> {
  if (!(caught instanceof RecordError)) return { message: "Unable to save the record." };
  if (caught.reason === "not-found") return { message: missingText };
  if (caught.reason === "stale") return { stale: true, message: staleText };
  if (caught.reason !== "rejected") return { message: "Unable to save the record." };
  // The server uses this code for the primary ID and for relation IDs. It
  // cannot be pinned to one field, and an update has no editable primary ID.
  if (caught.details.id?.includes("invalid_record_id")) return { message: "A record ID is invalid. Check the record ID and any related record IDs against their collection policies." };
  const errors: Record<string, string> = {};
  for (const [name, codes] of Object.entries(caught.details)) {
    if (name === "id") {
      errors.id = codes.includes("record_id_conflict") ? "This record ID is already in use." : "The record ID does not match this collection's identity policy.";
      continue;
    }
    const field = collection.fields.find((candidate) => candidate.name === name);
    if (field !== undefined) errors[name] = validationText(field, codes[0], field.relation === undefined ? undefined : catalog?.byID(field.relation.targetCollectionId)?.recordId);
  }
  return Object.keys(errors).length === 0 ? { message: "Record input is invalid." } : { errors };
}

function removeFailure(caught: unknown): Partial<EditorState> {
  if (!(caught instanceof RecordError)) return { message: "Unable to delete the record." };
  if (caught.reason === "not-found") return { message: missingText };
  if (caught.reason === "stale") return { stale: true, message: staleText };
  // The server reports a restricted relation against the record id, so the
  // per-field mapping a save uses cannot render it.
  if (caught.reason === "rejected" && caught.details["id"]?.includes("relation_delete_restricted")) {
    return { message: "Other records still reference this record. Remove those references first." };
  }
  return { message: "Unable to delete the record." };
}

export function useRecordEditor(collectionName: string, id: string | undefined, navigate: (route: AdminRoute) => void, hasSelectedFiles = false): RecordEditor {
  const { records, schema } = useBackend();
  const { state: session } = useSession();
  const identity = adminRouteIdentity({ kind: "record", collection: collectionName, id });
  const active = useRouteGuard(identity);
  const isCreate = id === undefined;
  const [state, update] = useResourceState(identity, initialEditor);
  const attempt = useRef(new CreateAttempt(collectionName));
  if (attempt.current.collection !== collectionName) attempt.current = new CreateAttempt(collectionName);
  // Keep established draft keys, reserving NUL (forbidden in server IDs) to
  // distinguish an existing text record named `new` from the creation draft.
  const scope: RecordScope = { administrator: session.kind === "authenticated" ? session.administrator.id : "", collection: collectionName, record: id === "new" ? "\0new" : id ?? "new" };

  // A reload asked for after a failed precondition keeps the unsaved draft;
  // every other load starts the editor over.
  const keepDraft = state.stale && state.collection !== null && state.loadedResource === identity;
  const keepDraftRef = useRef(keepDraft);
  keepDraftRef.current = keepDraft;

  useEffect(() => {
    if (session.kind !== "authenticated") return;
    const controller = new AbortController();
    const preserve = keepDraftRef.current;
    update(identity, preserve ? { message: null } : { message: null, loadedResource: null, collection: null, catalog: null, stale: false, loaded: null });
    void schema.catalog(controller.signal).then(async (catalog) => {
      const collection = catalog.get(collectionName);
      if (collection === undefined) throw new Error("missing collection");
      const record = id === undefined ? null : await records.get(collectionName, id, controller.signal);
      if (controller.signal.aborted) return;
      const fields = draftFields(collection, isCreate);
      const clean = initialDraft(fields, record?.data ?? {}, isCreate, relationPolicies(catalog));
      const restored = state.reloads === 0 ? readDraft(scope, fields) : null;
      const draft = preserve ? state.draft : restored === null ? clean : { ...clean, ...restored };
      update(identity, { loadedResource: identity, collection, catalog, draft, initial: clean, loaded: record, stale: false, errors: {} });
    }).catch((caught: unknown) => {
      if (controller.signal.aborted) return;
      update(identity, {
        message: preserve ? "Unable to reload the latest record. Your draft was preserved; try again."
          : caught instanceof RecordError && caught.reason === "not-found" ? missingText
            : "Unable to load the record editor.",
      });
    });
    return () => controller.abort();
  }, [collectionName, id, identity, isCreate, records, schema, session.kind, state.reloads, update, scope.administrator]);

  const dirty = useMemo(() => dirtyNames(state.draft, state.initial), [state.draft, state.initial]);
  const savedDraft = useUnsavedDraft(identity, dirty.size > 0 || hasSelectedFiles);
  useEffect(() => {
    if (state.collection === null || scope.administrator === "") return;
    if (dirty.size === 0) clearDraft(scope); else writeDraft(scope, state.draft, draftFields(state.collection, isCreate));
  }, [dirty.size, isCreate, scope.administrator, scope.collection, scope.record, state.collection, state.draft]);
  // The draft as it stands now, readable from a response handler that closed
  // over the draft it sent: the two differ exactly when editing continued.
  const live = useRef(state.draft);
  live.current = state.draft;

	const save = useCallback(async (files: Record<string, File[]> = {}) => {
    const collection = state.collection;
    if (collection === null) return;
    const fields = draftFields(collection, isCreate);
    const { input, errors } = encodeDraft(fields, state.draft, dirty, isCreate, relationPolicies(state.catalog));
    if (isCreate && collection.recordId?.kind === "text" && !collection.recordId.autogeneratePattern && input.id === undefined) errors.id = "Enter a record ID.";
    update(identity, { errors });
    if (Object.keys(errors).length > 0) return;
    update(identity, { pending: true, message: null });
    try {
      if (!isCreate && state.loaded === null) throw new Error("record version is unavailable");
			const withFiles = Object.values(files).some((selected) => selected.length > 0);
			const stored = isCreate || state.loaded === null
				? withFiles ? await records.createWithFiles(attempt.current, input, files) : await records.create(attempt.current, input)
				: withFiles ? await records.updateWithFiles(state.loaded, input, files) : await records.update(state.loaded, input);
      if (isCreate) attempt.current.reset();
      if (!active()) return;
      // The server accepted the draft it was sent, so that draft is the saved one
      // and its answer is the version a further save builds on. A draft edited
      // since is unsaved work of its own, so it keeps the guard and this page.
      const outran = dirtyNames(live.current, state.draft).size > 0;
      update(identity, { ...acceptedDraft(fields, state.draft, live.current), loaded: isCreate ? null : stored, message: outran ? (isCreate ? createdText : savedText) : null });
      if (outran) return;
      savedDraft();
      clearDraft(scope);
      navigate({ kind: "records", collection: collectionName });
    } catch (caught: unknown) {
      update(identity, saveFailure(caught, collection, state.catalog));
    } finally {
      update(identity, { pending: false });
    }
  }, [active, collectionName, dirty, identity, isCreate, navigate, records, savedDraft, state.catalog, state.collection, state.draft, state.loaded, update]);

  const remove = useCallback(async () => {
    const record = state.loaded;
    if (record === null || !window.confirm("Delete this record? This cannot be undone.")) return;
    update(identity, { pending: true, message: null });
    try {
      await records.remove(record);
      if (!active()) return;
      savedDraft();
      clearDraft(scope);
      navigate({ kind: "records", collection: collectionName });
    } catch (caught: unknown) {
      update(identity, removeFailure(caught));
    } finally {
      update(identity, { pending: false });
    }
  }, [active, collectionName, identity, navigate, records, savedDraft, state.loaded, update]);

  const setField = (field: FieldDefinition, next: FieldState) => update(identity, (current) => {
    const errors = { ...current.errors };
    delete errors[field.name];
    return { draft: { ...current.draft, [field.name]: next }, errors };
  });

  return {
    isCreate,
    collection: state.collection,
    fields: ordered(state.collection),
    errors: state.errors,
    message: state.message,
    pending: state.pending,
    stale: state.stale,
    recordID: state.draft.id?.kind === "value" ? String(state.draft.id.value) : "",
    setRecordID: (value) => {
      if (isCreate && state.collection?.recordId?.kind === "text") setField(recordIDField, value === "" ? { kind: "unset" } : { kind: "value", value });
    },
    targetOf: (field) => state.catalog === null || field.relation === undefined ? undefined : state.catalog.byID(field.relation.targetCollectionId),
    stateOf: (field) => state.draft[field.name] ?? { kind: "unset" },
    setKind: (field, kind) => setField(field, blankState(field, kind)),
    setValue: (field, value) => setField(field, { kind: "value", value }),
    reload: () => update(identity, (current) => ({ reloads: current.reloads + 1 })),
		save: (files) => void save(files),
    remove: () => void remove(),
    duplicate: () => {
      if (state.collection === null || isCreate) return;
      const duplicate = Object.fromEntries(Object.entries(state.draft).filter(([name]) => state.collection?.fields.find((field) => field.name === name)?.type !== "file" && !["password", "autodate"].includes(state.collection?.fields.find((field) => field.name === name)?.type ?? "")));
      if (!writeDraft({ ...scope, record: "new" }, duplicate, state.collection.fields)) {
        update(identity, { message: "Unable to prepare the duplicate because browser storage is unavailable. The original record is still open." });
        return;
      }
      navigate({ kind: "record", collection: collectionName });
    },
  };
}
