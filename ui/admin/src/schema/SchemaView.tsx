import { useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { RiArrowGoBackLine, RiPlayLine } from "@remixicon/react";
import { sampleView, truncateCollection, type Collection, type CollectionType } from "../api/collections";
import { ApiError, stringifyJson } from "../api/http";
import type { CollectionScaffolds } from "../api/schema";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Field } from "../components/Field";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { deleteCollection } from "./apply";
import { FieldPane } from "./FieldPane";
import { IndexPane } from "./IndexPane";
import { PlanReview } from "./PlanReview";
import { describeErrors, FieldRows, IndexRows, Section, ViewFieldRows } from "./SchemaRows";
import { useSchemaEditor, type SchemaEditor } from "./useSchemaEditor";

function CollectionSection({ editor }: { editor: SchemaEditor }) {
  const t = useT();
  const { draft, errors, creating } = editor;
  const error = (key: string) => (errors.collection[key] ? describeErrors(t, errors.collection[key]) : undefined);
  const recordId = draft.recordId;
  return (
    <Section title={t("schema.collection")}>
      <div className="schema-grid">
        {/* The auth registration keeps the old name on a catalog rename, so auth collections are not renamed here. */}
        <Field label={t("schema.collectionName")} mono spellCheck={false} autoComplete="off" required value={draft.name} error={error("name")} disabled={!creating && draft.type === "auth"}
          hint={creating ? undefined : t(draft.type === "auth" ? "schema.authRenameLocked" : "schema.renameCollectionHint")} onChange={(event) => editor.setMeta({ name: event.target.value })} />
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

// shortcut: the sample endpoint sends uuid columns as 16-byte arrays; shown as a uuid until the server encodes them.
function sampleCell(value: unknown) {
  if (value === null || value === undefined) return <span className="na">null</span>;
  if (Array.isArray(value) && value.length === 16 && value.every((byte) => typeof byte === "number")) {
    const hex = value.map((byte: number) => byte.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }
  return typeof value === "object" ? stringifyJson(value) : String(value);
}

function ViewSection({ editor, collection }: { editor: SchemaEditor; collection?: Collection }) {
  const t = useT();
  const sample = useMutation({ mutationFn: (query: string) => sampleView(query) });
  const declarative = collection?.view && !collection.view.sql;
  if (declarative) {
    const view = collection.view!;
    return (
      <Section title={t("schema.viewQuery")}>
        <p className="field-hint schema-note">{t("schema.declarativeView")}</p>
        <pre className="schema-code mono">{`source: ${view.source}\nprojection: ${(view.projection ?? []).join(", ") || "*"}${view.filter ? `\nfilter: ${stringifyJson(view.filter)}` : ""}`}</pre>
      </Section>
    );
  }
  const columns = sample.data ? ["id", ...sample.data.fields.map((field) => field.Name).filter((name) => name !== "id")] : [];
  const sampleError = sample.error instanceof ApiError && sample.error.code === "schema_conflict" ? t("schema.sampleRejected") : sample.error && errorText(t, sample.error);
  return (
    <Section title={t("schema.viewQuery")} action={<Button size="sm" variant="secondary" icon={RiPlayLine} loading={sample.isPending} disabled={!editor.draft.viewQuery.trim()} onClick={() => sample.mutate(editor.draft.viewQuery)}>{t("schema.sample")}</Button>}>
      <Field kind="textarea" label={t("schema.sqlLabel")} mono rows={8} spellCheck={false} maxLength={5000} placeholder="select id, title from app.posts where published" value={editor.draft.viewQuery}
        hint={t("schema.sqlHint")} error={sampleError || (editor.errors.collection.viewQuery && describeErrors(t, editor.errors.collection.viewQuery)) || undefined} onChange={(event) => editor.setMeta({ viewQuery: event.target.value })} />
      {sample.data && (
        <div className="schema-sample">
          <p className="field-hint">{t("schema.sampleResult", { columns: sample.data.fields.length, rows: sample.data.items?.length ?? 0 })}</p>
          <div className="grid-scroll">
            <table className="grid">
              <thead>
                <tr>
                  {columns.map((name) => {
                    const type = sample.data.fields.find((field) => field.Name === name)?.Type;
                    return <th key={name}>{name} {type && <span className="th-type">{type}</span>}</th>;
                  })}
                </tr>
              </thead>
              <tbody>
                {(sample.data.items ?? []).map((row, position) => (
                  <tr key={position}>
                    {columns.map((name) => <td key={name} className="mono">{sampleCell(row[name])}</td>)}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </Section>
  );
}

/** Truncate and delete, each confirmed by typing the collection name. */
function DangerZone({ collection, editor }: { collection: Collection; editor: SchemaEditor }) {
  const t = useT();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [action, setAction] = useState<"truncate" | "delete" | null>(null);
  const [typed, setTyped] = useState("");
  const close = () => {
    setAction(null);
    setTyped("");
  };
  const run = useMutation({
    mutationFn: async (kind: "truncate" | "delete") => {
      if (kind === "truncate") {
        const result = await truncateCollection(collection.name);
        return t("schema.truncated", { count: result.recordsDeleted });
      }
      const leftover = await deleteCollection(collection);
      if (leftover) toast(t("schema.authLeftover", { error: errorText(t, leftover) }), "danger");
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
        confirmDisabled={typed !== collection.name}
        onConfirm={() => action && run.mutate(action)}
        onCancel={close}
      >
        <Field label={t("schema.typeName", { name: collection.name })} mono autoComplete="off" spellCheck={false} value={typed} onChange={(event) => setTyped(event.target.value)} />
      </ConfirmDialog>
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
  const editableView = isView && (creating || Boolean(collection?.view?.sql));
  let pane = null;
  if (editor.reviewing) pane = <PlanReview editor={editor} />;
  else if (editor.openField) pane = <FieldPane key={editor.openField.key} editor={editor} field={editor.openField} catalog={catalog} />;
  else if (editor.openIndex) pane = <IndexPane key={editor.openIndex.key} editor={editor} index={editor.openIndex} />;

  const list = (
    <>
      <div className="toolbar schema-toolbar">
        <span className="schema-status" data-dirty={dirty ? "" : undefined}>
          {dirty ? t("schema.unsaved") : creating ? t("schema.draftNew") : t("schema.upToDate")}
        </span>
        {dirty && <Button size="sm" variant="transparent" icon={RiArrowGoBackLine} onClick={editor.discard}>{t("schema.discard")}</Button>}
        <Button size="sm" disabled={!dirty && !creating} onClick={() => editor.setPane({ review: true })}>{t("schema.reviewChanges")}</Button>
      </div>
      <div className="page-content schema-content">
        <CollectionSection editor={editor} />
        {isView ? (
          <>
            {(editableView || collection?.view) && <ViewSection editor={editor} collection={collection} />}
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
      <ConfirmDialog
        open={editor.blocker.state === "blocked"}
        title={t("schema.leaveTitle")}
        message={t("schema.leaveText")}
        confirmLabel={t("schema.discard")}
        danger
        onConfirm={editor.leave}
        onCancel={() => editor.blocker.reset?.()}
      />
    </>
  );
  return <SplitView list={list} pane={pane} paneLabel={t("schema.detail")} onClose={() => editor.setPane(null)} />;
}
