import { useEffect, useMemo, useRef, useState } from "react";
import type { Location } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { Collection } from "../api/collections";
import { ApiError } from "../api/http";
import { createRecord, createRecordWithFiles, deleteRecord, getRecord, updateRecord, type RecordData, type RecordWithETag } from "../api/records";
import { errorText, sessionQuery } from "../app/queries";
import { toast } from "../components/Toast";
import { en } from "../i18n/en";
import { useT, type Translate } from "../i18n";
import { buildInput, initialDrafts, isDirty, type Draft } from "./fields";
import { addToLists, recordQuery, removeFromLists, replaceInLists } from "./queries";
import { changedFields, clearDraft, readDraft, writeDraft, type DraftScope } from "./recordDraft";
import { expandFor } from "./relation";

const trackedParams = ["record", "new"];

/** True when a navigation would close or replace the open record (filter/sort changes do not). */
export function leavesRecord(current: Pick<Location, "pathname" | "search">, next: Pick<Location, "pathname" | "search">): boolean {
  if (current.pathname !== next.pathname) return true;
  const a = new URLSearchParams(current.search);
  const b = new URLSearchParams(next.search);
  return trackedParams.some((key) => a.get(key) !== b.get(key));
}

function validationText(t: Translate, code: string): string {
  const key = `validation.${code}`;
  return key in en ? t(key as keyof typeof en) : code.replaceAll("_", " ");
}

// getRandomValues also works on plain-HTTP hosts, where crypto.randomUUID is absent.
const randomKey = () => Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) => byte.toString(16).padStart(2, "0")).join("");

class DraftErrors extends Error {
  constructor(readonly fields: Record<string, string>) {
    super("invalid drafts");
  }
}

interface Options {
  collection: Collection;
  /** Absent while creating. */
  loaded?: RecordWithETag;
  onCreated: (id: string) => void;
  onDeleted: () => void;
}

/** Draft state, browser draft recovery, save (with If-Match), delete, conflict and the unsaved-changes guard props for one record. */
export function useRecordForm({ collection, loaded, onCreated, onDeleted }: Options) {
  const t = useT();
  const queryClient = useQueryClient();
  const admin = useQuery(sessionQuery).data?.administrator.id ?? "-";
  const record: RecordData | undefined = loaded?.record;
  const isNew = !loaded;
  const fields = collection.fields;
  const scope: DraftScope = useMemo(() => ({ admin, collection: collection.id, record: record?.id ?? "new" }), [admin, collection.id, record?.id]);
  const initial = useMemo(() => initialDrafts(fields, record), [fields, record]);
  // Edits left from a reload, a closed tab, a duplicate or a 412 reload, laid over the latest values.
  const [restored, setRestored] = useState(() => readDraft(scope, fields));
  const [drafts, setDrafts] = useState(() => ({ ...initial, ...restored?.fields }));
  const [files, setFiles] = useState<Record<string, File[]>>({});
  const [newId, setNewId] = useState("");
  const [progress, setProgress] = useState<number | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [conflict, setConflict] = useState(false);
  // Reused only to retry the same create after a transport failure.
  const idempotencyKey = useRef<string | null>(null);
  const dirty = isDirty(fields, drafts, initial) || Object.values(files).some((list) => list.length > 0) || newId !== "";
  // Set once the edits are saved or given up, so neither the guard nor the autosave brings them back.
  const settled = useRef(false);

  const persist = () => {
    if (settled.current) return;
    const changed = changedFields(fields, drafts, initial);
    if (changed) writeDraft(scope, changed);
    else clearDraft(scope);
  };
  const persistRef = useRef(persist);
  persistRef.current = persist;
  useEffect(() => {
    const timer = window.setTimeout(() => persistRef.current(), 400);
    return () => window.clearTimeout(timer);
  }, [drafts]);

  const settle = () => {
    settled.current = true;
    clearDraft(scope);
  };

  const onFailure = (error: unknown) => {
    setProgress(null);
    if (error instanceof DraftErrors) {
      setErrors(Object.fromEntries(Object.entries(error.fields).map(([name, code]) => [name, validationText(t, code)])));
      return;
    }
    if (error instanceof ApiError && error.status > 0) idempotencyKey.current = null;
    if (error instanceof ApiError && error.code === "precondition_failed") {
      persist();
      setConflict(true);
      return;
    }
    if (error instanceof ApiError && error.code === "validation_failed" && error.details && typeof error.details === "object") {
      const details = error.details as Record<string, string[]>;
      setErrors(Object.fromEntries(Object.entries(details).map(([name, codes]) => [name, codes.map((code) => validationText(t, code)).join(", ")])));
    }
    toast(errorText(t, error), "danger");
  };

  const save = useMutation({
    mutationFn: () => {
      const { input, errors: invalid } = buildInput(fields, drafts, initial, isNew);
      if (Object.keys(invalid).length) throw new DraftErrors(invalid);
      setErrors({});
      if (loaded) return updateRecord(collection.name, loaded.record.id, input, { ifMatch: loaded.etag || undefined });
      // Text-id collections take the new id from the form; empty lets the server generate one.
      if (newId.trim()) input.id = newId.trim();
      const key = (idempotencyKey.current ??= randomKey());
      if (!Object.values(files).some((list) => list.length > 0)) return createRecord(collection.name, input, { idempotencyKey: key });
      setProgress(0);
      return createRecordWithFiles(collection.name, input, files, { idempotencyKey: key, onProgress: setProgress });
    },
    onSuccess: (result) => {
      settle();
      setProgress(null);
      if (isNew) addToLists(queryClient, collection.name, result.record);
      else replaceInLists(queryClient, collection.name, result.record);
      // A save response has no expand data: fetch the row once more so relation labels follow the change.
      const expand = expandFor(collection);
      if (expand) {
        void getRecord(collection.name, result.record.id, { expand }).then(
          (fresh) => replaceInLists(queryClient, collection.name, fresh.record, true),
          () => undefined,
        );
      }
      queryClient.setQueryData(recordQuery(collection.name, result.record.id).queryKey, result);
      toast(t("records.saved"));
      if (isNew) onCreated(result.record.id);
    },
    onError: onFailure,
  });

  const remove = useMutation({
    mutationFn: () => deleteRecord(collection.name, loaded!.record.id, { ifMatch: loaded!.etag || undefined }),
    onSuccess: () => {
      settle();
      queryClient.removeQueries({ queryKey: recordQuery(collection.name, loaded!.record.id).queryKey });
      removeFromLists(queryClient, collection.name, [loaded!.record.id]);
      toast(t("records.deleted", { count: 1 }));
      onDeleted();
    },
    onError: onFailure,
  });

  /** Loads the latest version; with `keepEdits` the changed fields are laid over it again. */
  const reload = (keepEdits: boolean) => {
    if (keepEdits) persist();
    else settle();
    setConflict(false);
    if (loaded) void queryClient.invalidateQueries({ queryKey: recordQuery(collection.name, loaded.record.id).queryKey });
  };

  return {
    record,
    isNew,
    drafts,
    setDraft: (name: string, value: Draft) => setDrafts((previous) => ({ ...previous, [name]: value })),
    files,
    setFiles: (name: string, list: File[]) => setFiles((previous) => ({ ...previous, [name]: list })),
    progress,
    newId,
    setNewId,
    errors,
    dirty,
    conflict,
    reload,
    restoredAt: restored?.savedAt ?? null,
    /** Drops recovered edits and shows the stored values again. */
    dropRestored: () => {
      clearDraft(scope);
      setRestored(null);
      setDrafts(initial);
    },
    /** Called when the user agrees to lose the edits (discard dialog). */
    discard: settle,
    /** Prepares the create form with this record's values; false when browser storage refused it. */
    duplicate: () => {
      const values = changedFields(fields, drafts, initialDrafts(fields)) ?? {};
      return writeDraft({ ...scope, record: "new" }, values);
    },
    /** Back to the stored values (Discard). */
    reset: () => {
      clearDraft(scope);
      setRestored(null);
      setDrafts(initial);
      setFiles({});
      setNewId("");
      setErrors({});
    },
    save,
    remove,
    /** Props for the unsaved-changes guard: leaving the record asks; a reload keeps a local draft. */
    guard: { dirty, release: () => settled.current, leaves: leavesRecord, onDiscard: settle, onUnload: () => persistRef.current() },
  };
}
