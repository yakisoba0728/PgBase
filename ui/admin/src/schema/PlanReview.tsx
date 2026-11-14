import { useState } from "react";
import { Link } from "react-router";
import { ApiError } from "../api/http";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { untrackOperation } from "../app/operations";
import { ChangeList } from "../components/ChangeList";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Skeleton, Notice } from "../components/Feedback";
import { useT } from "../i18n";
import { Undecided } from "./apply";
import { dangerousStatement, DraftDrifted } from "./draft";
import { describeErrors } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";
import { PaneHeader } from "../components/SplitView";

/** The server's plan for the draft: what changes, the SQL it runs, and Apply. */
export function PlanReview({ editor }: { editor: SchemaEditor }) {
  const t = useT();
  const { planQuery, apply, errors } = editor;
  const [confirming, setConfirming] = useState(false);
  const close = () => editor.setPane(null);
  const sections = planQuery.data?.sections ?? [];
  const danger = sections.some((section) => section.items.some((item) => item.danger) || section.sql.some(dangerousStatement));
  const empty = sections.every((section) => section.items.length === 0 && section.sql.length === 0);
  const rowErrors = Object.keys(errors.fields).length + Object.keys(errors.indexes).length + Object.keys(errors.collection).length;
  const lost = apply.error instanceof Undecided || (apply.error instanceof ApiError && apply.error.code === "network_error");

  const risky = sections.flatMap((section) => section.items.filter((item) => item.danger).map((item) => `${t(`schema.kind.${item.kind}`)} ${item.name}`));
  const name = editor.draft.name.trim();

  let body;
  if (!editor.dirty && !editor.creating) {
    body = <EmptyState>{t("schema.noChanges")}</EmptyState>;
  } else if (editor.drift) {
    const { conflicts } = editor.drift;
    body = (
      <Notice title={t("schema.driftTitle")} actions={<Button size="sm" variant="secondary" onClick={editor.rebase}>{t("schema.driftRebase")}</Button>}>
        {t("schema.driftText")}
        {conflicts.length > 0 && <> {t("schema.driftConflicts", { names: conflicts.join(", ") })}</>}
      </Notice>
    );
  } else if (planQuery.error instanceof DraftDrifted) {
    body = <Skeleton variant="form" />;
  } else if (rowErrors || errors.other.length) {
    body = (
      <div className="pane-notice" data-tone="danger" role="alert">
        <strong>{t("schema.planRejected")}</strong>
        {rowErrors > 0 && <p>{t("schema.fixRows")}</p>}
        {Object.entries(errors.collection).map(([key, codes]) => <p key={key}>{key}: {describeErrors(t, codes)}</p>)}
        {errors.other.map((line) => <p key={line} className="mono">{line}</p>)}
      </div>
    );
  } else if (planQuery.isError) {
    body = (
      <Notice tone="danger" title={t("schema.planFailed")} actions={<Button size="sm" variant="secondary" onClick={() => planQuery.refetch()}>{t("common.retry")}</Button>}>
        {errorText(t, planQuery.error)}
      </Notice>
    );
  } else if (!planQuery.data) {
    body = <Skeleton variant="form" />;
  } else {
    body = (
      <>
        {danger && (
          <Notice tone="danger" title={t("schema.dangerTitle")}>{t("schema.dangerText")}</Notice>
        )}
        {editor.pending && (
          <Notice title={t("schema.pendingTitle")} actions={<>
            <Link to={`/schema-operations?op=${encodeURIComponent(editor.pending.id)}`} className="mono">{editor.pending.id}</Link>
            {/* The way out once the operation is checked as finished or abandoned. */}
            <Button size="sm" variant="secondary" onClick={() => untrackOperation(editor.pending!.id)}>{t("schema.pendingForget")}</Button>
          </>}>{t("schema.pendingText")}</Notice>
        )}
        {lost && (
          <Notice title={t(apply.error instanceof Undecided ? "schema.undecidedTitle" : "schema.lostTitle")}
            actions={<Button size="sm" variant="secondary" loading={apply.isPending} onClick={() => (danger ? setConfirming(true) : apply.mutate())}>{t("schema.checkAgain")}</Button>}>
            {t("schema.lostText")}
          </Notice>
        )}
        {empty && <p className="field-hint">{t("schema.planEmpty")}</p>}
        {sections.map((section, position) => (
          <ChangeList key={position} title={sections.length > 1 ? section.title : undefined} changes={section.items} sql={section.sql} dangerous={dangerousStatement} />
        ))}
      </>
    );
  }

  return (
    <>
      <PaneHeader title={t("schema.review")} onClose={close} />
      <div className="pane-body">{body}</div>
      <footer className="pane-footer">
        <Button variant="secondary" onClick={close}>{t("common.cancel")}</Button>
        <Button variant={danger ? "danger" : "primary"} loading={apply.isPending} disabled={!planQuery.data || planQuery.isFetching || Boolean(editor.drift) || Boolean(editor.pending) || rowErrors > 0 || (empty && !editor.creating)} onClick={() => (danger ? setConfirming(true) : apply.mutate())}>
          {t("schema.apply")}
        </Button>
      </footer>
      {/* Data-losing plans take a typed confirmation, like truncate and delete. */}
      <ConfirmDialog
        open={confirming}
        title={t("schema.dangerTitle")}
        message={t("schema.dangerText")}
        confirmLabel={t("schema.apply")}
        danger
        busy={apply.isPending}
        typed={{ phrase: name, label: t("schema.typeName", { name }) }}
        onConfirm={() => apply.mutate(undefined, { onSettled: () => setConfirming(false) })}
        onCancel={() => setConfirming(false)}
      >
        {risky.length > 0 && <ul className="schema-confirm-list mono">{risky.map((line) => <li key={line}>{line}</li>)}</ul>}
      </ConfirmDialog>
    </>
  );
}
