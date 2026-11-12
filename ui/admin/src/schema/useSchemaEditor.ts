import { useCallback, useEffect, useMemo, useRef } from "react";
import { useNavigate } from "react-router";
import { outside } from "../components/UnsavedGuard";
import { useSearchState } from "../app/search";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { planCollectionChange, type ApplyIdentity, type Collection, type CollectionType, type CreateCollectionChange, type FieldType } from "../api/collections";
import { ApiError, stringifyJson } from "../api/http";
import { previewSchemaImport, type CollectionScaffolds, type SchemaDocument } from "../api/schema";
import { isWatched, operations, trackOperation, untrackOperation, useOperations, watchOperation } from "../app/operations";
import { errorText } from "../app/queries";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { collectionsQuery } from "../collections/queries";
import { drafts, dropDraft, isDirty } from "./drafts";
import { applyCollection, applyImport, newIdentity, Undecided } from "./apply";
import {
  buildChange,
  diffPlan,
  draftFromCollection,
  DraftDrifted,
  DraftInvalid,
  emptyDraft,
  mapErrors,
  newField,
  newKey,
  rebaseDraft,
  sameDraft,
  type BuiltChange,
  type ChangeItem,
  type Draft,
  type DraftField,
  type DraftIndex,
  type MappedErrors,
} from "./draft";

/** New auth collections go through the schema document import, which also creates the auth policy. */
function authDocument(change: CreateCollectionChange, scaffolds: CollectionScaffolds): SchemaDocument {
  const { operation: _op, viewQuery: _v, view: _view, ...collection } = change;
  return {
    format: "pgbase.schema/v1",
    collections: [{ ...scaffolds.auth.collection, ...collection }],
    authCollections: [{ ...scaffolds.auth.authCollection, name: change.name, recordId: change.recordId }],
  };
}

interface PlanSection {
  title: string;
  items: ChangeItem[];
  sql: string[];
}

interface Options {
  collection?: Collection;
  type: CollectionType;
  catalog: Collection[];
  scaffolds?: CollectionScaffolds;
  /** Navigation inside this path (other tabs, panes) keeps the draft without asking. */
  basePath: string;
}

export function useSchemaEditor({ collection, type, catalog, scaffolds, basePath }: Options) {
  const t = useT();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { params, update: setSearch } = useSearchState();
  const key = collection?.id ?? `new:${type}`;
  const creating = !collection;
  const initial = useMemo(
    () => (collection ? draftFromCollection(collection, catalog) : emptyDraft(type, type === "auth" ? scaffolds?.auth.collection.fields : [])),
    [collection, catalog, type, scaffolds],
  );
  // An untouched draft simply follows the catalog; an edited one keeps the base it was edited from.
  const current = useCallback(
    (entry = drafts.get()[key]) => (entry && (creating || isDirty(entry) || sameDraft(entry.base, initial)) ? entry : { base: initial, draft: initial }),
    [key, creating, initial],
  );
  const { draft, base } = current(drafts.use()[key]);
  const dirty = !sameDraft(draft, base);
  // Set once the draft is applied or the collection deleted: no more guard, no more planning.
  const finished = useRef(false);
  // The collection changed on the server since this draft started: planning waits for a rebase.
  const drift = useMemo(
    () => (!creating && dirty && !finished.current && !sameDraft(base, initial) ? rebaseDraft(draft, base, initial) : undefined),
    [creating, dirty, draft, base, initial],
  );

  const update = useCallback(
    (change: (current: Draft) => Draft) => {
      const entry = current();
      drafts.set({ ...drafts.get(), [key]: { base: entry.base, draft: change(entry.draft) } });
    },
    [key, current],
  );

  // Pane state lives in the query string: ?field=, ?index= or ?review.
  const setPane = useCallback(
    (pane: { field?: string; index?: string; review?: boolean } | null) => setSearch({ field: pane?.field, index: pane?.index, review: pane?.review ? "1" : null }),
    [setSearch],
  );
  const openField = draft.fields.find((field) => field.key === params.get("field"));
  const openIndex = draft.indexes.find((index) => index.key === params.get("index"));
  const reviewing = params.has("review");

  const built = useMemo((): { ok?: BuiltChange; invalid?: Record<string, string[]> } => {
    try {
      return { ok: buildChange(draft, collection, base) };
    } catch (error) {
      if (error instanceof DraftInvalid) return { invalid: error.details };
      throw error;
    }
  }, [draft, collection, base]);

  const planQuery = useQuery({
    queryKey: ["schema-plan", key, built.ok ? stringifyJson(built.ok.change) : "", stringifyJson(base)],
    enabled: reviewing && !finished.current && !drift && Boolean(built.ok) && (dirty || creating) && (type !== "auth" || !creating || Boolean(scaffolds)),
    retry: false,
    gcTime: 0,
    queryFn: async ({ signal }) => {
      const change = built.ok!.change;
      if (creating && type === "auth" && change.operation === "create") {
        const preview = await previewSchemaImport(authDocument(change, scaffolds!), signal);
        // The import also lists the auth registration as a second, SQL-less change of the same collection.
        const unique = preview.changes.filter((plan, position, all) => all.findIndex((other) => other.definition.name === plan.definition.name) === position);
        const sections = unique.map((plan): PlanSection => ({ title: plan.definition.name, items: diffPlan(undefined, plan.definition), sql: plan.sql ?? [] }));
        return { kind: "import" as const, preview, sections, checksum: preview.checksum };
      }
      // The update sends the whole field list, so it is planned only against the catalog the draft was edited from.
      let latest = collection;
      if (collection) {
        const catalog = Object.values((await queryClient.fetchQuery({ ...collectionsQuery, staleTime: 0 })).collections);
        latest = catalog.find((candidate) => candidate.id === collection.id);
        if (!latest || !sameDraft(draftFromCollection(latest, catalog), base)) throw new DraftDrifted();
      }
      const plan = await planCollectionChange(change, signal);
      const sections: PlanSection[] = [{ title: plan.definition.name, items: diffPlan(latest, plan.definition), sql: plan.sql }];
      return { kind: "collection" as const, plan, change, sections, checksum: plan.checksum };
    },
  });

  const errors: MappedErrors = useMemo(() => {
    const keys = { fieldKeys: draft.fields.map((field) => field.key), indexKeys: draft.indexes.map((index) => index.key) };
    if (built.invalid) return mapErrors(built.invalid, keys, draft);
    const error = planQuery.error;
    if (error instanceof ApiError && error.code === "validation_failed" && built.ok) return mapErrors(error.details, built.ok, draft);
    return { fields: {}, indexes: {}, collection: {}, other: [] };
  }, [built, planQuery.error, draft]);

  // An applied update stays on this page; once its review has closed, planning may resume.
  useEffect(() => {
    if (!reviewing) finished.current = false;
  }, [reviewing]);

  // One identity per plan, persisted with the running-operations entry: it is reused only to
  // retry after a lost or undecided response, including after a reload.
  const subject = collection?.name ?? draft.name.trim();
  const tracked = useOperations();
  const pending = tracked.find((operation) => (operation.kind === "schema" || operation.kind === "import") && operation.subject === subject && operation.idempotencyKey);
  const apply = useMutation({
    mutationFn: async () => {
      const planned = planQuery.data;
      if (!planned) throw new Error("no plan");
      const kind = planned.kind === "import" ? "import" : "schema";
      const resumed = operations().find((operation) => operation.kind === kind && operation.subject === subject && operation.checksum === planned.checksum && operation.idempotencyKey);
      const identity: ApplyIdentity = resumed ? { operationId: resumed.id, idempotencyKey: resumed.idempotencyKey! } : newIdentity();
      trackOperation({ kind, id: identity.operationId, subject, checksum: planned.checksum, idempotencyKey: identity.idempotencyKey });
      const release = watchOperation(identity.operationId);
      try {
        if (planned.kind === "import") await applyImport(planned.preview, identity);
        else await applyCollection(planned.change, planned.plan, identity);
        untrackOperation(identity.operationId);
      } catch (error) {
        if (!(error instanceof Undecided) && !(error instanceof ApiError && error.code === "network_error")) untrackOperation(identity.operationId);
        throw error;
      } finally {
        release();
      }
      return planned.kind === "import" ? planned.preview.document.collections[0].name : planned.plan.definition.name;
    },
    onSuccess: async (name) => {
      finished.current = true;
      await queryClient.invalidateQueries({ queryKey: ["collections"] });
      toast(t("schema.applied"));
      await navigate(`/collections/${encodeURIComponent(name)}/schema`, { replace: !creating });
      dropDraft(key);
    },
    onError: (error) => {
      if (error instanceof Undecided || (error instanceof ApiError && error.code === "network_error")) return;
      if (error instanceof ApiError && error.code === "schema_conflict") void planQuery.refetch();
      toast(errorText(t, error), "danger");
    },
  });

  const names = draft.fields.map((field) => field.name);
  return {
    key,
    creating,
    draft,
    base,
    dirty,
    errors,
    openField,
    openIndex,
    reviewing,
    planQuery,
    /** Set when the server's collection moved on since the draft started; `conflicts` name edits the latest version overrides. */
    drift,
    /** Replays the draft's edits onto the latest catalog state. */
    rebase: () => drift && drafts.set({ ...drafts.get(), [key]: { base: initial, draft: drift.draft } }),
    apply,
    /** An earlier apply of this collection whose outcome is still open (another plan, another tab, or before a reload). */
    pending: pending && !isWatched(pending.id) && pending.checksum !== planQuery.data?.checksum ? pending : undefined,
    /** Props for the unsaved-changes guard; moving between this collection's tabs keeps the draft. */
    guard: { dirty, release: () => finished.current, leaves: outside(basePath), onDiscard: () => dropDraft(key) },
    setPane,
    setMeta: (patch: Partial<Draft>) => update((current) => ({ ...current, ...patch })),
    addField: (fieldType: FieldType) => {
      const field = newField(fieldType, names);
      update((current) => ({ ...current, fields: [...current.fields, field] }));
      setPane({ field: field.key });
    },
    updateField: (fieldKey: string, patch: Partial<DraftField>) =>
      update((current) => ({ ...current, fields: current.fields.map((field) => (field.key === fieldKey ? { ...field, ...patch } : field)) })),
    removeField: (fieldKey: string) => {
      update((current) => ({ ...current, fields: current.fields.filter((field) => field.key !== fieldKey) }));
      setPane(null);
    },
    moveField: (fieldKey: string, step: -1 | 1) =>
      update((current) => {
        const fields = [...current.fields];
        const from = fields.findIndex((field) => field.key === fieldKey);
        const to = from + step;
        if (from < 0 || to < 0 || to >= fields.length) return current;
        [fields[from], fields[to]] = [fields[to], fields[from]];
        return { ...current, fields };
      }),
    addIndex: () => {
      const index: DraftIndex = { key: newKey("i"), name: `idx_${draft.name || "new"}_${draft.indexes.length + 1}`, unique: false, keys: [] };
      update((current) => ({ ...current, indexes: [...current.indexes, index] }));
      setPane({ index: index.key });
    },
    updateIndex: (indexKey: string, patch: Partial<DraftIndex>) =>
      update((current) => ({ ...current, indexes: current.indexes.map((index) => (index.key === indexKey ? { ...index, ...patch } : index)) })),
    removeIndex: (indexKey: string) => {
      update((current) => ({ ...current, indexes: current.indexes.filter((index) => index.key !== indexKey) }));
      setPane(null);
    },
    discard: () => {
      dropDraft(key);
      setPane(null);
    },
    /** Lets the next navigation through (after a delete). */
    release: () => {
      finished.current = true;
      dropDraft(key);
    },
  };
}

export type SchemaEditor = ReturnType<typeof useSchemaEditor>;
