import { useState } from "react";
import { useNavigate } from "react-router";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { truncateCollection, type Collection, type CollectionType } from "../api/collections";
import type { CollectionScaffolds } from "../api/schema";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Field } from "../components/Field";
import { SaveBar } from "../components/SaveBar";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { deleteCollection } from "./apply";
import { FieldPane } from "./FieldPane";
import { IndexPane } from "./IndexPane";
import { PlanReview } from "./PlanReview";
import { describeErrors, FieldRows, IndexRows, Section, ViewFieldRows } from "./SchemaRows";
import { ViewSection } from "./ViewEditor";
import { useSchemaEditor, type SchemaEditor } from "./useSchemaEditor";
import "../styles/schema.css";

function CollectionSection({ editor }: { editor: SchemaEditor }) {
  const t = useT();
  const { draft, errors, creating } = editor;
  const error = (key: string) => (errors.collection[key] ? describeErrors(t, errors.collection[key]) : undefined);
  const recordId = draft.recordId;
  return (
    <Section title={t("schema.collection")}>
      <div className="schema-grid">
        <Field label={t("schema.collectionName")} mono spellCheck={false} autoComplete="off" required value={draft.name} error={error("name")}
          hint={creating ? undefined : t(draft.type === "auth" ? "schema.renameAuthHint" : "schema.renameCollectionHint")} onChange={(event) => editor.setMeta({ name: event.target.value })} />
        {draft.type !== "view" && (
          <>
            <Field label={t("schema.compatibilityName")} mono spellCheck={false} value={draft.compatibilityName} error={error("compatibilityName")} hint={t("schema.compatibilityHint")}
              onChange={(event) => editor.setMeta({ compatibilityName: event.target.value })} />
            <Field kind="select" label={t("schema.validationMode")} value={draft.validationMode} error={error("validationMode")} onChange={(event) => editor.setMeta({ validationMode: event.target.value as typeof draft.validationMode })}
              options={[{ value: "postgres", label: t("schema.mode.postgres") }, { value: "pocketbase", label: t("schema.mode.pocketbase") }]} />
            <Field kind="select" label={t("schema.recordId")} value={recordId.kind} disabled={!creating} error={error("recordId")} hint={creating ? t("schema.recordIdHint") : undefined}
              onChange={(event) => editor.setMeta({ recordId: event.target.value === "text" ? { kind: "text", minLength: 15, maxLength: 15, pattern: "^[a-z0-9]+$", autogeneratePattern: "[a-z0-9]{15}" } : { kind: "uuid" } })}
              options={[{ value: "uuid", label: t("schema.recordId.uuid") }, { value: "text", label: t("schema.recordId.text") }]} />
            {recordId.kind === "text" && (
              <>
                <div className="schema-pair">
                  <Field label={t("schema.recordId.minLength")} type="number" min={1} max={255} mono disabled={!creating} value={recordId.minLength ?? ""}
                    onChange={(event) => editor.setMeta({ recordId: { ...recordId, minLength: event.target.value === "" ? undefined : Number(event.target.value) } })} />
                  <Field label={t("schema.recordId.maxLength")} type="number" min={1} max={255} mono disabled={!creating} value={recordId.maxLength ?? ""}
                    onChange={(event) => editor.setMeta({ recordId: { ...recordId, maxLength: event.target.value === "" ? undefined : Number(event.target.value) } })} />
                </div>
                <Field label={t("schema.recordId.pattern")} mono disabled={!creating} value={recordId.pattern ?? ""} onChange={(event) => editor.setMeta({ recordId: { ...recordId, pattern: event.target.value } })} />
                <Field label={t("schema.recordId.autogenerate")} mono disabled={!creating} value={recordId.autogeneratePattern ?? ""} onChange={(event) => editor.setMeta({ recordId: { ...recordId, autogeneratePattern: event.target.value } })} />
              </>
            )}
          </>
        )}
      </div>
    </Section>
  );
}

/** Truncate and delete, each confirmed by typing the collection name. */
function DangerZone({ collection, editor }: { collection: Collection; editor: SchemaEditor }) {
  const t = useT();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [action, setAction] = useState<"truncate" | "delete" | null>(null);
  const close = () => {
    setAction(null);
  };
  const run = useMutation({
    mutationFn: async (kind: "truncate" | "delete") => {
      if (kind === "truncate") {
        const result = await truncateCollection(collection.name);
        return t("schema.truncated", { count: result.recordsDeleted });
      }
      await deleteCollection(collection);
      return t("schema.deleted", { name: collection.name });
    },
    onSuccess: async (message, kind) => {
      close();
      toast(message);
      if (kind === "delete") {
        editor.release();
        await queryClient.invalidateQueries({ queryKey: ["collections"] });
        navigate("/collections", { replace: true });
      } else {
        await queryClient.invalidateQueries({ queryKey: ["records", collection.name] });
      }
    },
    onError: (error) => toast(errorText(t, error), "danger"),
  });
  return (
    <Section title={t("schema.danger")}>
      <div className="schema-danger">
        {collection.type !== "view" && (
          <div>
            <Button size="sm" variant="transparent" className="txt-danger" onClick={() => setAction("truncate")}>{t("schema.truncate")}</Button>
            <span className="field-hint">{t("schema.truncateHint")}</span>
          </div>
        )}
        <div>
          <Button size="sm" variant="transparent" className="txt-danger" onClick={() => setAction("delete")}>{t("schema.delete")}</Button>
          <span className="field-hint">{t("schema.deleteHint")}</span>
        </div>
      </div>
      <ConfirmDialog
        open={action !== null}
        title={t(action === "delete" ? "schema.deleteTitle" : "schema.truncateTitle", { name: collection.name })}
        message={t(action === "delete" ? "schema.deleteText" : "schema.truncateText")}
        confirmLabel={t(action === "delete" ? "schema.delete" : "schema.truncate")}
        danger
        busy={run.isPending}
        typed={{ phrase: collection.name, label: t("schema.typeName", { name: collection.name }) }}
        onConfirm={() => action && run.mutate(action)}
        onCancel={close}
      />
    </Section>
  );
}

interface SchemaViewProps {
  collection?: Collection;
  type: CollectionType;
  catalog: Collection[];
  scaffolds?: CollectionScaffolds;
  basePath: string;
}

/** The Schema tab (and the new-collection editor): a local draft, reviewed as a server plan, then applied. */
export function SchemaView({ collection, type, catalog, scaffolds, basePath }: SchemaViewProps) {
  const t = useT();
  const editor = useSchemaEditor({ collection, type, catalog, scaffolds, basePath });
  const { draft, dirty, creating } = editor;
  const isView = draft.type === "view";
  let pane = null;
  if (editor.reviewing) pane = <PlanReview editor={editor} />;
  else if (editor.openField) pane = <FieldPane key={editor.openField.key} editor={editor} field={editor.openField} catalog={catalog} />;
  else if (editor.openIndex) pane = <IndexPane key={editor.openIndex.key} editor={editor} index={editor.openIndex} />;

  const list = (
    <>
      <div className="toolbar">
        <SaveBar dirty={dirty} clean={creating ? t("schema.draftNew") : undefined} onDiscard={editor.discard} onSave={() => editor.setPane({ review: true })} saveLabel={t("schema.reviewChanges")} saveDisabled={!dirty && !creating} />
      </div>
      <div className="page-content schema-content">
        <CollectionSection editor={editor} />
        {isView ? (
          <>
            <ViewSection editor={editor} catalog={catalog} />
            {collection && <ViewFieldRows fields={collection.fields} />}
          </>
        ) : (
          <>
            <FieldRows editor={editor} />
            <IndexRows editor={editor} />
          </>
        )}
        {collection && <DangerZone collection={collection} editor={editor} />}
      </div>
      <UnsavedGuard {...editor.guard} />
    </>
  );
  return <SplitView list={list} pane={pane} paneLabel={t("schema.detail")} onClose={() => editor.setPane(null)} />;
}
