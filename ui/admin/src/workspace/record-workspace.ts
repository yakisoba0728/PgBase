import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import {
  CreateAttempt,
  RecordError,
  type CollectionCatalog,
  type CollectionDefinition,
  type FieldDefinition,
  type RecordPage,
  type VersionedRecord,
} from "../backend/ports";
import { adminRouteIdentity, useResourceState, useRouteGuard, type AdminRoute } from "../routing/admin-location";
import {
  blankState,
  dirtyNames,
  encodeDraft,
  initialDraft,
  relationTargetOption,
  relationTargetSearch,
  relationTargetSearchField,
  selectRelationTarget,
  validationText,
  type Draft,
  type FieldState,
  type RelationTargetOption,
} from "./record-codec";

const staleText = "This record changed after you loaded it. Reload the latest version before saving or deleting.";
const missingText = "This record no longer exists.";
const alwaysSortable = new Set(["id", "created_at"]);

function ordered(collection: CollectionDefinition | null): FieldDefinition[] {
  return collection === null ? [] : collection.fields.slice().sort((left, right) => left.ordinal - right.ordinal);
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
  sort: string;
  // The cursor that opened every page after the first, oldest first. Empty
  // means the first page, so its length is also how far the chain has walked.
  cursors: string[];
}

function initialList(identity: string): ListState {
  return { identity, collection: null, page: null, failed: false, filterInvalid: false, filterText: "", appliedFilter: "", sort: "", cursors: [] };
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
  hasPrevious: boolean;
  fieldFor(column: string): FieldDefinition | undefined;
  sortDirection(column: string): "ascending" | "descending" | undefined;
  sortable(column: string): boolean;
  setFilterText(text: string): void;
  applyFilter(): void;
  clear(): void;
  toggleSort(column: string): void;
  previousPage(): void;
  nextPage(): void;
}

export function useRecordList(collectionName: string): RecordList {
  const { records, schema } = useBackend();
  const identity = adminRouteIdentity({ kind: "records", collection: collectionName });
  const [state, update] = useResourceState(identity, initialList);
  const inFlight = useRef<AbortController | null>(null);

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
      const page = await records.list(collectionName, { cursor: cursors.at(-1), filter: state.appliedFilter, sort: state.sort }, controller.signal);
      if (!controller.signal.aborted) update(identity, { page, cursors, filterInvalid: false, failed: false });
    } catch (caught: unknown) {
      if (!controller.signal.aborted) update(identity, rejected(caught) ? { filterInvalid: true } : { failed: true });
    }
  }, [collectionName, identity, records, state.appliedFilter, state.sort, update]);

  // A new query has no chain to walk: its cursors address the old query's pages.
  useEffect(() => {
    update(identity, { page: null, cursors: [] });
    void openPage([]);
    return () => inFlight.current?.abort();
  }, [identity, openPage, update]);

  const fields = state.collection?.fields;
  return {
    collection: state.collection,
    columns: state.collection === null ? [] : ["id", "created_at", "updated_at", ...ordered(state.collection).map((field) => field.name)],
    page: state.page,
    failed: state.failed,
    filterInvalid: state.filterInvalid,
    filterText: state.filterText,
    filtered: state.appliedFilter !== "" || state.sort !== "",
    hasPrevious: state.cursors.length > 0,
    fieldFor: (column) => fields?.find((field) => field.name === column),
    sortDirection: (column) => state.sort === column ? "ascending" : state.sort === `-${column}` ? "descending" : undefined,
    sortable: (column) => alwaysSortable.has(column) || (fields?.some((field) =>
      field.name === column &&
      field.type !== "jsonb" &&
      (field.type !== "relation" || field.relation?.cardinality === "single") &&
      (field.indexed || field.unique),
    ) ?? false),
    setFilterText: (filterText) => update(identity, { filterText }),
    applyFilter: () => update(identity, (current) => ({ appliedFilter: current.filterText.trim() })),
    clear: () => update(identity, { filterText: "", appliedFilter: "", sort: "" }),
    toggleSort: (column) => update(identity, (current) => ({ sort: nextSort(current.sort, column) })),
    previousPage: () => { if (state.cursors.length > 0) void openPage(state.cursors.slice(0, -1)); },
    nextPage: () => { if (state.page?.nextCursor !== undefined) void openPage([...state.cursors, state.page.nextCursor]); },
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
  targetOf(field: FieldDefinition): CollectionDefinition | undefined;
  stateOf(field: FieldDefinition): FieldState;
  setKind(field: FieldDefinition, kind: FieldState["kind"]): void;
  setValue(field: FieldDefinition, value: string | boolean): void;
  reload(): void;
  save(): void;
  remove(): void;
}

function saveFailure(caught: unknown, collection: CollectionDefinition): Partial<EditorState> {
  if (!(caught instanceof RecordError)) return { message: "Unable to save the record." };
  if (caught.reason === "not-found") return { message: missingText };
  if (caught.reason === "stale") return { stale: true, message: staleText };
  if (caught.reason !== "rejected") return { message: "Unable to save the record." };
  const errors: Record<string, string> = {};
  for (const [name, codes] of Object.entries(caught.details)) {
    const field = collection.fields.find((candidate) => candidate.name === name);
    if (field !== undefined) errors[name] = validationText(field, codes[0]);
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

export function useRecordEditor(collectionName: string, id: string | undefined, navigate: (route: AdminRoute) => void): RecordEditor {
  const { records, schema } = useBackend();
  const identity = adminRouteIdentity({ kind: "record", collection: collectionName, id });
  const active = useRouteGuard(identity);
  const isCreate = id === undefined;
  const [state, update] = useResourceState(identity, initialEditor);
  const attempt = useRef(new CreateAttempt(collectionName));
  if (attempt.current.collection !== collectionName) attempt.current = new CreateAttempt(collectionName);

  // A reload asked for after a failed precondition keeps the unsaved draft;
  // every other load starts the editor over.
  const keepDraft = state.stale && state.collection !== null && state.loadedResource === identity;
  const keepDraftRef = useRef(keepDraft);
  keepDraftRef.current = keepDraft;

  useEffect(() => {
    const controller = new AbortController();
    const preserve = keepDraftRef.current;
    update(identity, preserve ? { message: null } : { message: null, loadedResource: null, collection: null, catalog: null, stale: false, loaded: null });
    void schema.catalog(controller.signal).then(async (catalog) => {
      const collection = catalog.get(collectionName);
      if (collection === undefined) throw new Error("missing collection");
      const record = id === undefined ? null : await records.get(collectionName, id, controller.signal);
      if (controller.signal.aborted) return;
      const draft = initialDraft(collection.fields, record?.data ?? {}, isCreate);
      update(identity, { loadedResource: identity, collection, catalog, draft, initial: draft, loaded: record, stale: false, errors: {} });
    }).catch((caught: unknown) => {
      if (controller.signal.aborted) return;
      update(identity, {
        message: preserve ? "Unable to reload the latest record. Your draft was preserved; try again."
          : caught instanceof RecordError && caught.reason === "not-found" ? missingText
            : "Unable to load the record editor.",
      });
    });
    return () => controller.abort();
  }, [collectionName, id, identity, isCreate, records, schema, state.reloads, update]);

  const dirty = useMemo(() => dirtyNames(state.draft, state.initial), [state.draft, state.initial]);

  const save = useCallback(async () => {
    const collection = state.collection;
    if (collection === null) return;
    const { input, errors } = encodeDraft(collection.fields, state.draft, dirty, isCreate);
    update(identity, { errors });
    if (Object.keys(errors).length > 0) return;
    update(identity, { pending: true, message: null });
    try {
      if (isCreate) {
        await records.create(attempt.current, input);
        attempt.current.reset();
      } else {
        if (state.loaded === null) throw new Error("record version is unavailable");
        await records.update(state.loaded, input);
      }
      if (!active()) return;
      navigate({ kind: "records", collection: collectionName });
    } catch (caught: unknown) {
      update(identity, saveFailure(caught, collection));
    } finally {
      update(identity, { pending: false });
    }
  }, [active, collectionName, dirty, identity, isCreate, navigate, records, state.collection, state.draft, state.loaded, update]);

  const remove = useCallback(async () => {
    const record = state.loaded;
    if (record === null || !window.confirm("Delete this record? This cannot be undone.")) return;
    update(identity, { pending: true, message: null });
    try {
      await records.remove(record);
      if (!active()) return;
      navigate({ kind: "records", collection: collectionName });
    } catch (caught: unknown) {
      update(identity, removeFailure(caught));
    } finally {
      update(identity, { pending: false });
    }
  }, [active, collectionName, identity, navigate, records, state.loaded, update]);

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
    targetOf: (field) => state.catalog === null || field.relation === undefined ? undefined : state.catalog.byID(field.relation.targetCollectionId),
    stateOf: (field) => state.draft[field.name] ?? { kind: "unset" },
    setKind: (field, kind) => setField(field, blankState(field, kind)),
    setValue: (field, value) => setField(field, { kind: "value", value }),
    reload: () => update(identity, (current) => ({ reloads: current.reloads + 1 })),
    save: () => void save(),
    remove: () => void remove(),
  };
}

interface RelationPicker {
  query: string;
  results: RelationTargetOption[] | null;
  status: string | null;
  searching: boolean;
  searchField: FieldDefinition | undefined;
  setQuery(query: string): void;
  search(): void;
  select(id: string): void;
}

export function useRelationPicker(target: CollectionDefinition, field: FieldDefinition, draft: string, onChange: (value: string) => void): RelationPicker {
  const { records } = useBackend();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<RelationTargetOption[] | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const inFlight = useRef<AbortController | null>(null);

  useEffect(() => () => inFlight.current?.abort(), []);

  const search = useCallback(async () => {
    inFlight.current?.abort();
    inFlight.current = null;
    setSearching(false);
    const request = relationTargetSearch(target, query);
    if ("problem" in request) {
      setResults(null);
      setStatus(request.problem === "invalid-uuid"
        ? "Enter a canonical, non-zero target UUID to search."
        : "The target search value is too long or unsupported.");
      return;
    }
    const controller = new AbortController();
    inFlight.current = controller;
    setSearching(true);
    setStatus(null);
    try {
      const page = await records.list(target.name, { filter: request.filter, sort: request.sort }, controller.signal);
      if (controller.signal.aborted) return;
      const options = page.items.flatMap((record) => {
        const option = relationTargetOption(record, request.displayField);
        return option === undefined ? [] : [option];
      });
      setResults(options);
      setStatus(options.length === 0 ? "No matching target records."
        : page.nextCursor ? "Showing the first 50 matching target records." : null);
    } catch {
      if (!controller.signal.aborted) {
        setResults(null);
        setStatus("Unable to search target records.");
      }
    } finally {
      if (!controller.signal.aborted) setSearching(false);
    }
  }, [query, records, target]);

  return {
    query,
    results,
    status,
    searching,
    searchField: relationTargetSearchField(target),
    setQuery,
    search: () => void search(),
    select: (id) => {
      const selection = selectRelationTarget(field, draft, id);
      if (selection.status === "selected") { onChange(selection.draft); setStatus(`Selected ${id}.`); }
      else if (selection.status === "duplicate") setStatus("That target is already selected.");
      else setStatus(`Select no more than ${field.relation?.maxSelect ?? 0} related records.`);
    },
  };
}
