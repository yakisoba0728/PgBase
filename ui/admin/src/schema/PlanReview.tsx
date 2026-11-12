import { Link } from "react-router";
import { ApiError } from "../api/http";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { untrackOperation } from "../app/operations";
import { ChangeList } from "../components/ChangeList";
import { EmptyState, Loader, Notice } from "../components/Feedback";
import { useT } from "../i18n";
import { Undecided } from "./apply";
import { dangerousStatement } from "./draft";
import { describeErrors } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";
import { PaneHeader } from "../components/SplitView";

/** The server's plan for the draft: what changes, the SQL it runs, and Apply. */
export function PlanReview({ editor }: { editor: SchemaEditor }) {
  const t = useT();
  const { planQuery, apply, errors } = editor;
  const close = () => editor.setPane(null);
  const sections = planQuery.data?.sections ?? [];
  const danger = sections.some((section) => section.items.some((item) => item.danger) || section.sql.some(dangerousStatement));
  const empty = sections.every((section) => section.items.length === 0 && section.sql.length === 0);
  const rowErrors = Object.keys(errors.fields).length + Object.keys(errors.indexes).length + Object.keys(errors.collection).length;
  const lost = apply.error instanceof Undecided || (apply.error instanceof ApiError && apply.error.code === "network_error");

  let body;
  if (!editor.dirty && !editor.creating) {
    body = <EmptyState>{t("schema.noChanges")}</EmptyState>;
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
    body = <Loader />;
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
            actions={<Button size="sm" variant="secondary" loading={apply.isPending} onClick={() => apply.mutate()}>{t("schema.checkAgain")}</Button>}>
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
        <Button variant={danger ? "danger" : "primary"} loading={apply.isPending} disabled={!planQuery.data || planQuery.isFetching || Boolean(editor.pending) || rowErrors > 0 || (empty && !editor.creating)} onClick={() => apply.mutate()}>
          {t("schema.apply")}
        </Button>
      </footer>
    </>
  );
}
