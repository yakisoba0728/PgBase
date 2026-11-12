import { RiAlertLine, RiCloseLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { EmptyState, Loader } from "../components/Feedback";
import { useT } from "../i18n";
import { Undecided } from "./apply";
import { dangerousStatement } from "./draft";
import { describeErrors } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";

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
      <div className="pane-notice" data-tone="danger" role="alert">
        <strong>{t("schema.planFailed")}</strong>
        <p>{errorText(t, planQuery.error)}</p>
        <Button size="sm" variant="secondary" onClick={() => planQuery.refetch()}>{t("common.retry")}</Button>
      </div>
    );
  } else if (!planQuery.data) {
    body = <Loader />;
  } else {
    body = (
      <>
        {danger && (
          <div className="pane-notice" data-tone="danger" role="alert">
            <strong>{t("schema.dangerTitle")}</strong>
            <p>{t("schema.dangerText")}</p>
          </div>
        )}
        {lost && (
          <div className="pane-notice" role="alert">
            <strong>{t(apply.error instanceof Undecided ? "schema.undecidedTitle" : "schema.lostTitle")}</strong>
            <p>{t("schema.lostText")}</p>
            <Button size="sm" variant="secondary" loading={apply.isPending} onClick={() => apply.mutate()}>{t("schema.checkAgain")}</Button>
          </div>
        )}
        {empty && <p className="field-hint">{t("schema.planEmpty")}</p>}
        {sections.map((section, position) => (
          <section key={position} className="schema-plan">
            {sections.length > 1 && <h3 className="schema-plan-title mono">{section.title}</h3>}
            {section.items.length > 0 && (
              <ul className="schema-changes">
                {section.items.map((item, position) => (
                  <li key={position} data-kind={item.kind} data-danger={item.danger ? "" : undefined}>
                    <span className="schema-change-kind">{t(`schema.kind.${item.kind}`)}</span>
                    <span className="schema-change-target">{t(`schema.target.${item.target}`)}</span>
                    <span className="mono">{item.kind === "rename" ? `${item.detail} → ${item.name}` : item.name}</span>
                    {item.kind !== "rename" && item.detail && <span className="schema-change-detail mono">{item.detail}</span>}
                    {item.danger && <RiAlertLine size={14} aria-label={t("schema.dataLoss")} />}
                  </li>
                ))}
              </ul>
            )}
            {section.sql.length > 0 && (
              <details className="schema-sql" open={section.sql.some(dangerousStatement)}>
                <summary>{t("schema.sql", { count: section.sql.length })}</summary>
                {section.sql.map((statement, position) => (
                  <pre key={position} className="mono" data-danger={dangerousStatement(statement) ? "" : undefined}>{statement}</pre>
                ))}
              </details>
            )}
          </section>
        ))}
      </>
    );
  }

  return (
    <>
      <header className="pane-header">
        <h2 className="pane-title">{t("schema.review")}</h2>
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={close} />
      </header>
      <div className="pane-body">{body}</div>
      <footer className="pane-footer">
        <Button variant="secondary" onClick={close}>{t("common.cancel")}</Button>
        <Button variant={danger ? "danger" : "primary"} loading={apply.isPending} disabled={!planQuery.data || planQuery.isFetching || rowErrors > 0 || (empty && !editor.creating)} onClick={() => apply.mutate()}>
          {t("schema.apply")}
        </Button>
      </footer>
    </>
  );
}
