import { useCallback, useEffect, useMemo, useRef } from "react";
import { useBackend, useSession } from "../auth/session";
import {
  ApplyAttempt,
  CollectionCatalog,
  type CollectionChange,
  type CollectionDefinition,
  type CompoundIndex,
  type SchemaDocument,
} from "../backend/ports";
import {
  operationFailureMessage,
  useOperationRecovery,
} from "../operations/operation-recovery";
import {
  adminRouteIdentity,
  useResourceState,
  useRouteGuard,
  type AdminRoute,
} from "../routing/admin-location";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import { recoverabilityWindowMs } from "./pending-schema-operation-store";
import {
  appliedText,
  applyEdit,
  buildInput,
  catalogMovedText,
  createdText,
  draftMessage,
  draftSignature,
  newField,
  previewField,
  relationTargets,
  schemaMessage,
  type DesignerMode,
  type DraftEdit,
  type DraftField,
  type RelationTargetOption,
} from "./collection-draft";

// Keep the designer module as the stable public interface for its callers.
export {
  buildInput,
  previewField,
  type DesignerMode,
  type DraftEdit,
  type DraftField,
  type RelationTargetOption,
} from "./collection-draft";

import {
  authImportDocument,
  deleteTargetOf,
  initial,
  loadedState,
  pendingStore,
  recoverabilityText,
  redrafted,
  renamed,
} from "./collection-designer-state";
import {
  applyFailure,
  deleteFailure,
} from "./collection-designer-errors";
import type { CollectionDesigner } from "./collection-designer-contract";

export type { CollectionDesigner } from "./collection-designer-contract";

export function useCollectionDesigner(
  mode: DesignerMode,
  navigate: (route: AdminRoute) => void,
): CollectionDesigner {
  const { schema } = useBackend();
  const session = useSession().state;
  const administratorId =
    session.kind === "authenticated" ? session.administrator.id : null;
  const identity = adminRouteIdentity(
    mode.kind === "create"
      ? { kind: "collection-create" }
      : mode.kind === "duplicate"
        ? { kind: "collection-duplicate", collection: mode.collection }
        : mode.kind === "scaffold"
          ? { kind: "collection-scaffold", scaffold: mode.scaffold }
          : { kind: "collection-edit", collection: mode.collection },
  );
  const active = useRouteGuard(identity);
  const [state, update] = useResourceState(identity, (current) =>
    initial(mode, current),
  );
  // Retries reuse one identity per approved plan; a fresh preview does not.
  const attempt = useRef(new ApplyAttempt());
  const issued = useRef<string | null>(null);
  const stored =
    administratorId === null || state.pending || issued.current === identity
      ? null
      : pendingStore.load(administratorId, identity);
  const storageBlocked = stored === false,
    restored = stored || null;
  const undecided = state.undecided ?? restored;
  const recovery = useOperationRecovery(undecided?.operationId ?? null);
  const signature = draftSignature(
    state.name,
    state.fields,
    state.indexes,
    state.view,
    state.collectionType,
  );
  const live = useRef(signature);
  live.current = signature;
  const recovered = recovery?.status === "committed" ? undecided : null;
  const recoveryFailed = recovery?.status === "failed" && undecided !== null;
  const editingBlocked =
    state.terminal ||
    storageBlocked ||
    (undecided !== null && recovery?.status !== "failed");
  const ledgerOutran =
    recovered?.kind === "change" &&
    recovered.draft !== null &&
    recovered.draft !== signature;
  let unsaved = signature !== state.pristine;
  if (
    undecided?.kind === "change" &&
    recovery !== null &&
    undecided.draft !== null
  ) {
    if (recovery.status === "committed")
      unsaved = signature !== undecided.draft;
    else if (recovery.undecided) unsaved = true;
  }
  const savedDraft = useUnsavedDraft(identity, unsaved);

  const wantsCatalog =
    state.catalog === null &&
    !state.targetsFailed &&
    state.phase !== "missing" &&
    state.phase !== "unavailable" &&
    (!state.create ||
      state.duplicateSource !== undefined ||
      state.scaffold !== undefined ||
      state.view !== undefined ||
      state.fields.some((field) => field.type === "relation"));

  useEffect(() => {
    if (!wantsCatalog) return;
    const controller = new AbortController();
    update(identity, { targetsLoading: true });
    const exported =
      state.duplicateSource === undefined
        ? Promise.resolve(undefined)
        : schema.exportDocument?.(controller.signal);
    void Promise.all([
      schema.catalog(controller.signal),
      state.scaffold === undefined
        ? Promise.resolve(undefined)
        : schema.scaffolds?.(controller.signal),
      exported,
    ])
      .then(([catalog, scaffolds, document]) => {
        if (!controller.signal.aborted)
          update(identity, (current) =>
            loadedState(current, catalog, scaffolds, document),
          );
      })
      .catch(() => {
        if (controller.signal.aborted) return;
        update(identity, (current) =>
          current.create
            ? { targetsLoading: false, targetsFailed: true }
            : { phase: "unavailable" },
        );
      });
    return () => controller.abort();
  }, [
    identity,
    schema,
    state.duplicateSource,
    state.scaffold,
    update,
    wantsCatalog,
  ]);

  useEffect(() => {
    if (
      undecided === null ||
      recovery?.status === "committed" ||
      recovery?.status === "failed"
    )
      return;
    const expire = () => {
      if (
        pendingStore.forget(administratorId, identity, undecided.operationId)
      ) {
        issued.current = null;
        const next =
          administratorId === null
            ? null
            : pendingStore.load(administratorId, identity);
        update(identity, {
          undecided: next || null,
          plan: null,
          confirmed: false,
          deletePlan: null,
          deleteConfirmText: "",
        });
      }
    };
    const delay = undecided.expiresAt - Date.now();
    if (delay <= 0) {
      expire();
      return;
    }
    const timer = window.setTimeout(expire, delay);
    return () => window.clearTimeout(timer);
  }, [administratorId, identity, recovery?.status, undecided, update]);

  useEffect(() => {
    if (
      recovery?.status === "failed" &&
      undecided !== null &&
      pendingStore.forget(administratorId, identity, undecided.operationId)
    ) {
      issued.current = null;
      const next =
          administratorId === null
            ? null
            : pendingStore.load(administratorId, identity),
        message = operationFailureMessage(
          recovery.failureCode,
          "The schema operation did not apply.",
        );
      if (next === false) update(identity, { undecided: null });
      else if (next !== null)
        update(identity, {
          undecided: next,
          ...(undecided.kind === "drop"
            ? { deleteError: message }
            : { error: message }),
        });
      else if (state.undecided === null) update(identity, { undecided });
    }
    if (recovered === null) return;
    pendingStore.forget(administratorId, identity, recovered.operationId);
    if (recovered.draft !== null && signature === recovered.draft) savedDraft();
    navigate(
      recovered.kind === "drop" || state.create
        ? { kind: "collections" }
        : { kind: "records", collection: state.definition?.name ?? state.name },
    );
  }, [
    administratorId,
    identity,
    navigate,
    recovered,
    recovery?.status,
    savedDraft,
    signature,
    state.create,
    state.definition?.name,
    state.name,
    state.undecided,
    update,
  ]);

  const input = useMemo(() => {
    if (!state.create && state.definition === null) return null;
    try {
      return buildInput(
        state.create ? "create" : "update",
        state.name,
        state.fields,
        state.indexes,
        state.view,
        state.collectionType,
      );
    } catch {
      return null;
    }
  }, [
    state.create,
    state.definition,
    state.fields,
    state.indexes,
    state.view,
    state.name,
    state.collectionType,
  ]);
  const preview =
    state.plan !== null && input !== null && state.plan.matches(input)
      ? state.plan
      : null;
  const authPreview =
    state.authPlan !== null && state.authPlan.signature === signature
      ? state.authPlan
      : null;
  const previewDetails = authPreview ?? preview;
  const removedFields = useMemo(() => {
    if (previewDetails === null || state.definition === null) return [];
    const planned = new Set(
      previewDetails.fields.map((field) => field.id ?? field.name),
    );
    return state.definition.fields
      .filter((field) => !planned.has(field.id ?? field.name))
      .sort((left, right) => left.ordinal - right.ordinal)
      .map((field) => field.name);
  }, [previewDetails, state.definition]);
  const existing = useMemo(
    () => new Set(state.definition?.fields.map((field) => field.name) ?? []),
    [state.definition],
  );

  const requestPreview = useCallback(async () => {
    if (
      editingBlocked ||
      state.pending ||
      (!state.create && state.definition === null)
    )
      return;
    if (state.create && state.collectionType === "auth") {
      if (schema.previewImport === undefined) {
        update(identity, {
          error: "This server cannot preview an authentication collection.",
          authPlan: null,
        });
        return;
      }
      let document: SchemaDocument;
      try {
        document = authImportDocument(state);
      } catch (caught: unknown) {
        update(identity, {
          error: draftMessage(
            caught,
            "Authentication collection input is invalid.",
          ),
          authPlan: null,
        });
        return;
      }
      update(identity, {
        error: null,
        pending: true,
        plan: null,
        authPlan: null,
      });
      try {
        const planned = await schema.previewImport(document);
        const definition = planned.changes.find(
          (change) => change.definition.name === state.name,
        )?.definition;
        update(identity, {
          authPlan: {
            plan: planned,
            signature,
            baseVersion: planned.baseVersion,
            fields: definition?.fields ?? [],
            sql: planned.changes.flatMap((change) => change.sql),
          },
          confirmed: false,
          deletePlan: null,
          deleteConfirmText: "",
        });
      } catch (caught: unknown) {
        update(identity, {
          error: schemaMessage(
            caught,
            "Unable to preview the authentication collection.",
          ),
          authPlan: null,
        });
      } finally {
        update(identity, { pending: false });
      }
      return;
    }
    let change: CollectionChange;
    try {
      change = buildInput(
        state.create ? "create" : "update",
        state.name,
        state.fields,
        state.indexes,
        state.view,
        state.collectionType,
      );
    } catch (caught: unknown) {
      update(identity, {
        error: draftMessage(caught, "Collection input is invalid."),
      });
      return;
    }
    update(identity, { error: null, pending: true });
    try {
      const planned = await schema.plan(change);
      if (!state.create && planned.baseVersion !== state.catalog?.version) {
        update(identity, {
          error: catalogMovedText,
          plan: null,
          confirmed: false,
        });
        return;
      }
      update(identity, {
        plan: planned,
        authPlan: null,
        confirmed: false,
        deletePlan: null,
        deleteConfirmText: "",
        ...(recoveryFailed ? { undecided: null } : {}),
      });
    } catch (caught: unknown) {
      const fallback = state.create
        ? "Unable to preview the collection."
        : "Unable to preview the collection change.";
      update(identity, {
        error: schemaMessage(caught, fallback),
        plan: null,
        authPlan: null,
        confirmed: false,
      });
    } finally {
      update(identity, { pending: false });
    }
  }, [
    editingBlocked,
    identity,
    recoveryFailed,
    schema,
    signature,
    state,
    update,
  ]);

  const requestApply = useCallback(async () => {
    if (
      editingBlocked ||
      recoveryFailed ||
      previewDetails === null ||
      state.pending ||
      (removedFields.length > 0 && !state.confirmed)
    )
      return;
    if (authPreview !== null) {
      if (schema.applyImport === undefined) {
        update(identity, {
          error: "This server cannot apply an authentication collection.",
        });
        return;
      }
      update(identity, { pending: true, error: null });
      try {
        const result = await schema.applyImport(authPreview.plan);
        if (!active()) return;
        if (result.status !== "committed") {
          update(identity, {
            error: `Authentication collection import ${result.status}. Check schema operations before retrying.`,
          });
          return;
        }
        const outran = live.current !== signature;
        update(identity, {
          pristine: signature,
          terminal: !outran,
          authPlan: outran ? authPreview : null,
          error: outran ? createdText : null,
        });
        if (!outran) savedDraft();
        if (outran) return;
        navigate({ kind: "collections" });
      } catch (caught: unknown) {
        update(identity, {
          error: schemaMessage(
            caught,
            "Unable to create the authentication collection.",
          ),
        });
      } finally {
        update(identity, { pending: false });
      }
      return;
    }
    if (preview === null) return;
    update(identity, { pending: true, error: null, undecided: null });
    issued.current = identity;
    const pending = {
      kind: "change" as const,
      operationId: attempt.current.identityFor(preview).operationId,
      draft: signature,
      expiresAt: Date.now() + recoverabilityWindowMs,
    };
    if (
      administratorId === null ||
      !pendingStore.remember(administratorId, identity, pending)
    ) {
      issued.current = null;
      update(identity, { pending: false, error: recoverabilityText });
      return;
    }
    try {
      const outcome = await schema.apply(preview, attempt.current);
      if (outcome.kind === "applied")
        pendingStore.forget(administratorId, identity, pending.operationId);
      if (!active()) return;
      if (outcome.kind === "undecided") {
        update(identity, { undecided: pending });
        return;
      }
      const outran = live.current !== signature;
      const catalog =
        !state.create && outcome.definition !== undefined
          ? new CollectionCatalog(
              preview.baseVersion + 1,
              state.catalog
                ?.all()
                .map((collection) =>
                  collection.name === state.definition?.name
                    ? outcome.definition!
                    : collection,
                ) ?? [outcome.definition],
            )
          : state.catalog;
      update(identity, {
        pristine: signature,
        catalog,
        definition: state.create
          ? null
          : (outcome.definition ?? state.definition),
        terminal: state.create || outcome.definition === undefined,
        error: outran ? (state.create ? createdText : appliedText) : null,
      });
      // Only a response-supplied baseline can keep an outrunning edit draft.
      if (outran && !state.create && outcome.definition !== undefined) return;
      if (!outran) savedDraft();
      navigate(
        state.create
          ? { kind: "collections" }
          : {
              kind: "records",
              collection: state.definition?.name ?? state.name,
            },
      );
    } catch (caught: unknown) {
      pendingStore.forget(administratorId, identity, pending.operationId);
      update(identity, applyFailure(caught, state.create));
    } finally {
      issued.current = null;
      update(identity, { pending: false });
    }
  }, [
    active,
    administratorId,
    authPreview,
    editingBlocked,
    identity,
    navigate,
    preview,
    previewDetails,
    recoveryFailed,
    removedFields.length,
    savedDraft,
    schema,
    signature,
    state.confirmed,
    state.create,
    state.definition,
    state.name,
    state.pending,
    update,
  ]);

  const deleteTarget = useMemo(
    () =>
      recoveryFailed
        ? null
        : deleteTargetOf(state.definition, state.deletePlan),
    [recoveryFailed, state.definition, state.deletePlan],
  );

  const requestDeletePlan = useCallback(async () => {
    if (editingBlocked || state.definition === null || state.deletePending)
      return;
    update(identity, {
      deletePending: true,
      deletePlan: null,
      deleteConfirmText: "",
      deleteError: null,
    });
    try {
      const planned = await schema.plan({
        operation: "delete",
        name: state.definition.name,
      });
      if (planned.baseVersion !== state.catalog?.version) {
        update(identity, { deleteError: catalogMovedText });
        return;
      }
      update(identity, {
        deletePlan: planned,
        plan: null,
        confirmed: false,
        ...(recoveryFailed ? { undecided: null } : {}),
      });
    } catch (caught: unknown) {
      update(identity, {
        deletePlan: null,
        deleteError: schemaMessage(
          caught,
          "Unable to preview the collection deletion.",
        ),
      });
    } finally {
      update(identity, { deletePending: false });
    }
  }, [
    editingBlocked,
    identity,
    recoveryFailed,
    schema,
    state.definition,
    state.deletePending,
    update,
  ]);

  const requestDelete = useCallback(async () => {
    if (
      editingBlocked ||
      recoveryFailed ||
      deleteTarget === null ||
      state.deletePlan === null ||
      state.deletePending
    )
      return;
    if (state.deleteConfirmText !== deleteTarget.collection) return;
    update(identity, { deletePending: true, undecided: null });
    issued.current = identity;
    const pending = {
      kind: "drop" as const,
      operationId: attempt.current.identityFor(state.deletePlan).operationId,
      draft: signature,
      expiresAt: Date.now() + recoverabilityWindowMs,
    };
    if (
      administratorId === null ||
      !pendingStore.remember(administratorId, identity, pending)
    ) {
      issued.current = null;
      update(identity, {
        deletePending: false,
        deleteError: recoverabilityText,
      });
      return;
    }
    try {
      const outcome = await schema.apply(state.deletePlan, attempt.current);
      if (outcome.kind === "applied")
        pendingStore.forget(administratorId, identity, pending.operationId);
      if (!active()) return;
      if (outcome.kind === "undecided") {
        update(identity, { undecided: pending });
        return;
      }
      update(identity, {
        terminal: true,
        deletePlan: null,
        deleteConfirmText: "",
      });
      if (live.current === signature) savedDraft();
      navigate({ kind: "collections" });
    } catch (caught: unknown) {
      pendingStore.forget(administratorId, identity, pending.operationId);
      update(identity, deleteFailure(caught));
    } finally {
      issued.current = null;
      update(identity, { deletePending: false });
    }
  }, [
    active,
    administratorId,
    deleteTarget,
    editingBlocked,
    identity,
    navigate,
    recoveryFailed,
    savedDraft,
    schema,
    signature,
    state.deleteConfirmText,
    state.deletePending,
    state.deletePlan,
    update,
  ]);

  return {
    phase: state.phase,
    create: state.create,
    name: state.name,
    tableName: state.tableName,
    fields: state.fields,
    indexes: state.indexes ?? state.definition?.indexes ?? [],
    indexesTouched: state.indexes !== undefined,
    view: state.view,
    viewSources: (state.catalog?.all() ?? []).filter(
      (collection) => collection.view === undefined,
    ),
    collectionType:
      state.collectionType ?? (state.view === undefined ? "base" : "view"),
    isView: state.definition?.view !== undefined,
    isSQLView:
      state.definition?.view !== undefined && "sql" in state.definition.view,
    relationTargets: relationTargets(state.catalog, state.name),
    relationTargetsLoading: state.targetsLoading,
    relationTargetsFailed: state.targetsFailed,
    isNewField: (name) => !state.create && !existing.has(name),
    preview: previewDetails,
    removedFields,
    destructiveConfirmed: state.confirmed,
    applyBlocked:
      editingBlocked ||
      recoveryFailed ||
      (removedFields.length > 0 && !state.confirmed),
    editingBlocked,
    pending: state.pending,
    error: storageBlocked
      ? recoverabilityText
      : (state.error ??
        (ledgerOutran ? (state.create ? createdText : appliedText) : null)),
    deleteTarget,
    deleteConfirmText: state.deleteConfirmText,
    deleteConfirmed:
      deleteTarget !== null &&
      state.deleteConfirmText === deleteTarget.collection,
    deletePending: state.deletePending,
    deleteError: state.deleteError,
    recovery,
    rename: (name) => {
      if (!editingBlocked)
        update(identity, (current) => renamed(current, name));
    },
    editField: (index, edit) => {
      if (!editingBlocked)
        update(identity, (current) =>
          redrafted(
            current.fields.map((field, position) =>
              position === index ? applyEdit(field, edit, current.name) : field,
            ),
          ),
        );
    },
    addField: () => {
      if (!editingBlocked)
        update(identity, (current) =>
          redrafted([...current.fields, newField()]),
        );
    },
    removeField: (index) => {
      if (!editingBlocked)
        update(identity, (current) =>
          current.fields[index]?.system
            ? {}
            : redrafted(
                current.fields.filter((_, position) => position !== index),
              ),
        );
    },
    setIndexes: (indexes) => {
      if (!editingBlocked)
        update(identity, {
          indexes,
          plan: null,
          authPlan: null,
          confirmed: false,
        });
    },
    setView: (view) => {
      if (!editingBlocked)
        update(identity, {
          view,
          plan: null,
          authPlan: null,
          confirmed: false,
        });
    },
    confirmDestructive: (confirmed) => update(identity, { confirmed }),
    confirmDeleteName: (deleteConfirmText) =>
      update(identity, { deleteConfirmText }),
    requestPreview: () => void requestPreview(),
    requestApply: () => void requestApply(),
    requestDeletePlan: () => void requestDeletePlan(),
    cancelDelete: () =>
      update(identity, {
        deletePlan: null,
        deleteConfirmText: "",
        deleteError: null,
      }),
    requestDelete: () => void requestDelete(),
  };
}
