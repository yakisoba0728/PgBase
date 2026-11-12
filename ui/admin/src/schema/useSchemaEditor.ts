import { useCallback, useEffect, useMemo, useRef } from "react";
import { useBlocker, useNavigate, useSearchParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { planCollectionChange, type ApplyIdentity, type Collection, type CollectionType, type CreateCollectionChange, type FieldType } from "../api/collections";
import { ApiError, stringifyJson } from "../api/http";
import { previewSchemaImport, type CollectionScaffolds, type SchemaDocument } from "../api/schema";
import { createStore } from "../app/store";
import { errorText } from "../app/queries";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { applyCollection, applyImport, newIdentity, Undecided } from "./apply";
import {
  buildChange,
  diffPlan,
  draftFromCollection,
  DraftInvalid,
  emptyDraft,
  mapErrors,
  newField,
  newKey,
  sameDraft,
  type BuiltChange,
  type ChangeItem,
  type Draft,
  type DraftField,
  type DraftIndex,
  type MappedErrors,
} from "./draft";

// Drafts live outside React so switching between the collection's tabs keeps them.
const drafts = createStore<Record<string, { base: Draft; draft: Draft }>>({});

function dropDraft(key: string) {
  const { [key]: _dropped, ...rest } = drafts.get();
  drafts.set(rest);
}

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
  const [params, setParams] = useSearchParams();
  const key = collection?.id ?? `new:${type}`;
  const creating = !collection;
  const initial = useMemo(
    () => (collection ? draftFromCollection(collection, catalog) : emptyDraft(type, type === "auth" ? scaffolds?.auth.collection.fields : [])),
    [collection, catalog, type, scaffolds],
  );
  const entry = drafts.use()[key] ?? { base: initial, draft: initial };
  const { draft, base } = entry;
  const dirty = !sameDraft(draft, base);

  const update = useCallback(
    (change: (current: Draft) => Draft) => {
      const current = drafts.get()[key] ?? { base: initial, draft: initial };
      drafts.set({ ...drafts.get(), [key]: { base: current.base, draft: change(current.draft) } });
    },
    [key, initial],
  );

  // Pane state lives in the query string: ?field=, ?index= or ?review.
  const setPane = useCallback(
    (pane: { field?: string; index?: string; review?: boolean } | null) =>
      setParams((previous) => {
        const next = new URLSearchParams(previous);
        ["field", "index", "review"].forEach((name) => next.delete(name));
        if (pane?.field) next.set("field", pane.field);
        if (pane?.index) next.set("index", pane.index);
        if (pane?.review) next.set("review", "1");
        return next;
      }),
    [setParams],
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

  // Set once the draft is applied or the collection deleted: no more guard, no more planning.
  const finished = useRef(false);
  const planQuery = useQuery({
    queryKey: ["schema-plan", key, built.ok ? stringifyJson(built.ok.change) : ""],
    enabled: reviewing && !finished.current && Boolean(built.ok) && (dirty || creating) && (type !== "auth" || !creating || Boolean(scaffolds)),
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
      const plan = await planCollectionChange(change, signal);
      const sections: PlanSection[] = [{ title: plan.definition.name, items: diffPlan(collection, plan.definition), sql: plan.sql }];
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

  const blocker = useBlocker(({ nextLocation }) => dirty && !finished.current && nextLocation.pathname !== basePath && !nextLocation.pathname.startsWith(`${basePath}/`));
  // An applied update stays on this page; once its review has closed, planning may resume.
  useEffect(() => {
    if (!reviewing) finished.current = false;
  }, [reviewing]);
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  // One identity per plan; it is reused only to retry after a lost or undecided response.
  const identities = useRef(new Map<string, ApplyIdentity>());
  const apply = useMutation({
    mutationFn: async () => {
      const planned = planQuery.data;
      if (!planned) throw new Error("no plan");
      const identity = identities.current.get(planned.checksum) ?? newIdentity();
      identities.current.set(planned.checksum, identity);
      try {
        if (planned.kind === "import") await applyImport(planned.preview, identity);
        else await applyCollection(planned.change, planned.plan, identity);
      } catch (error) {
        if (!(error instanceof Undecided) && !(error instanceof ApiError && error.code === "network_error")) identities.current.delete(planned.checksum);
        throw error;
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
    apply,
    blocker,
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
    leave: () => {
      dropDraft(key);
      blocker.proceed?.();
    },
    /** Lets the next navigation through (after a delete). */
    release: () => {
      finished.current = true;
      dropDraft(key);
    },
  };
}

export type SchemaEditor = ReturnType<typeof useSchemaEditor>;
