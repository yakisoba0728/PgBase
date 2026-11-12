import { useEffect, useMemo, useRef, useState } from "react";
import { useBlocker, type Location } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { Collection } from "../api/collections";
import { ApiError } from "../api/http";
import { createRecord, deleteRecord, updateRecord, type RecordData, type RecordWithETag } from "../api/records";
import { errorText } from "../app/queries";
import { toast } from "../components/Toast";
import { en } from "../i18n/en";
import { useT, type Translate } from "../i18n";
import { buildInput, initialDrafts, isDirty, type Draft } from "./fields";
import { recordQuery } from "./queries";

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

/** Draft state, save (with If-Match), delete, conflict and unsaved-changes guard for one record. */
export function useRecordForm({ collection, loaded, onCreated, onDeleted }: Options) {
  const t = useT();
  const queryClient = useQueryClient();
  const record: RecordData | undefined = loaded?.record;
  const isNew = !loaded;
  const fields = collection.fields;
  const initial = useMemo(() => initialDrafts(fields, record), [fields, record]);
  const [drafts, setDrafts] = useState(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [conflict, setConflict] = useState(false);
  // Reused only to retry the same create after a transport failure.
  const idempotencyKey = useRef<string | null>(null);
  const dirty = isDirty(fields, drafts, initial);

  const skipGuard = useRef(false);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && !skipGuard.current && leavesRecord(currentLocation, nextLocation));
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  const refreshList = () => queryClient.invalidateQueries({ queryKey: ["records", collection.name, "list"] });

  const onFailure = (error: unknown) => {
    if (error instanceof DraftErrors) {
      setErrors(Object.fromEntries(Object.entries(error.fields).map(([name, code]) => [name, validationText(t, code)])));
      return;
    }
    if (error instanceof ApiError && error.status > 0) idempotencyKey.current = null;
    if (error instanceof ApiError && error.code === "precondition_failed") {
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
      return loaded
        ? updateRecord(collection.name, loaded.record.id, input, { ifMatch: loaded.etag || undefined })
        : createRecord(collection.name, input, { idempotencyKey: (idempotencyKey.current ??= randomKey()) });
    },
    onSuccess: (result) => {
      queryClient.setQueryData(recordQuery(collection.name, result.record.id).queryKey, result);
      void refreshList();
      toast(t("records.saved"));
      if (isNew) {
        skipGuard.current = true;
        onCreated(result.record.id);
      }
    },
    onError: onFailure,
  });

  const remove = useMutation({
    mutationFn: () => deleteRecord(collection.name, loaded!.record.id, { ifMatch: loaded!.etag || undefined }),
    onSuccess: () => {
      skipGuard.current = true;
      queryClient.removeQueries({ queryKey: recordQuery(collection.name, loaded!.record.id).queryKey });
      void refreshList();
      toast(t("records.deleted", { count: 1 }));
      onDeleted();
    },
    onError: onFailure,
  });

  const reload = () => {
    setConflict(false);
    if (loaded) void queryClient.invalidateQueries({ queryKey: recordQuery(collection.name, loaded.record.id).queryKey });
  };

  return {
    record,
    isNew,
    drafts,
    setDraft: (name: string, value: Draft) => setDrafts((previous) => ({ ...previous, [name]: value })),
    errors,
    dirty,
    conflict,
    reload,
    save,
    remove,
    blocker,
  };
}
