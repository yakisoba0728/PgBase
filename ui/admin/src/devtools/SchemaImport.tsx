import { useEffect, useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAlertLine, RiFileUploadLine } from "@remixicon/react";
import { ApiError, parseJson } from "../api/http";
import { previewSchemaImport, type SchemaDocument } from "../api/schema";
import { errorText } from "../app/queries";
import { collectionsQuery } from "../collections/queries";
import { Button } from "../components/Button";
import { EmptyState, Loader } from "../components/Feedback";
import { Checkbox } from "../components/Field";
import { toast } from "../components/Toast";
import { useT, type MessageKey, type Translate } from "../i18n";
import { newIdentity, Undecided } from "../schema/apply";
import { dangerousStatement } from "../schema/draft";
import { en } from "../i18n/en";
import { importSections, problemList, readPending, writePending, type PendingImport } from "./model";
import { runImport } from "./queries";

class InvalidJson extends Error {}

const codeText = (t: Translate, code: string) => (`schema.error.${code}` in en ? t(`schema.error.${code}` as MessageKey) : code);

/** Paste or load a document, preview the server's plan, then apply exactly that preview. */
export function SchemaImport() {
  const t = useT();
  const queryClient = useQueryClient();
  const catalog = useQuery(collectionsQuery);
  const [text, setText] = useState("");
  const [deleteMissing, setDeleteMissing] = useState(false);
  const [pending, setPending] = useState<PendingImport | null>(readPending);

  const preview = useMutation({
    mutationFn: (input: { text: string; deleteMissing: boolean }) => {
      let document: SchemaDocument;
      try {
        document = parseJson(input.text) as SchemaDocument;
      } catch {
        throw new InvalidJson();
      }
      return previewSchemaImport({ ...document, deleteMissing: input.deleteMissing || undefined });
    },
  });

  const apply = useMutation({
    mutationFn: (next: PendingImport) => {
      writePending(next);
      setPending(next);
      return runImport(next);
    },
    onSuccess: (result) => {
      writePending(null);
      setPending(null);
      preview.reset();
      void queryClient.invalidateQueries({ queryKey: ["collections"] });
      void queryClient.invalidateQueries({ queryKey: ["schema-export"] });
      toast(result.version === undefined ? t("devtools.import.applied") : t("devtools.import.appliedVersion", { version: result.version }));
    },
    onError: (error) => {
      // Only a definite answer ends the operation; a lost or undecided one stays resumable.
      if (error instanceof Undecided || (error instanceof ApiError && error.code === "network_error")) return;
      writePending(null);
      setPending(null);
    },
  });

  // After a reload, ask again about an apply whose answer this tab never saw.
  useEffect(() => {
    const stored = readPending();
    if (stored) apply.mutate(stored);
  }, []); // mount only

  const loadFile = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (file) void file.text().then(setText, () => toast(t("devtools.import.readFailed"), "danger"));
  };

  const runPreview = () => {
    // A document that asks for deletion shows it in the checkbox rather than hiding it.
    let wants = deleteMissing;
    try {
      wants ||= (parseJson(text) as SchemaDocument | null)?.deleteMissing === true;
    } catch {
      // Reported by the preview itself.
    }
    setDeleteMissing(wants);
    preview.mutate({ text, deleteMissing: wants });
  };

  const plan = preview.data;
  const stale = plan !== undefined && (preview.variables?.text !== text || preview.variables?.deleteMissing !== deleteMissing);
  const sections = plan && catalog.data ? importSections(plan, catalog.data) : [];
  const danger = sections.some((section) => section.danger);
  const lost = apply.error instanceof Undecided || (apply.error instanceof ApiError && apply.error.code === "network_error");

  let body;
  if (pending && (apply.isPending || lost)) {
    body = (
      <div className="pane-notice" role="status">
        <strong>{apply.isPending ? t("devtools.import.applying") : t(apply.error instanceof Undecided ? "schema.undecidedTitle" : "schema.lostTitle")}</strong>
        <p className="mono dt-muted">{pending.identity.operationId}</p>
        {!apply.isPending && (
          <>
            <p>{t("schema.lostText")}</p>
            <Button size="sm" variant="secondary" onClick={() => apply.mutate(pending)}>{t("schema.checkAgain")}</Button>
          </>
        )}
      </div>
    );
  } else if (preview.isPending) {
    body = <Loader />;
  } else if (preview.error) {
    const problems = preview.error instanceof ApiError ? problemList(preview.error.details) : [];
    body = (
      <div className="pane-notice" data-tone="danger" role="alert">
        <strong>{t("devtools.import.rejected")}</strong>
        <p>{preview.error instanceof InvalidJson ? t("schema.error.invalid_json") : errorText(t, preview.error)}</p>
        {problems.length > 0 && (
          <ul className="dt-problems">
            {problems.map((problem) => (
              <li key={problem.path}>
                <span className="mono">{problem.path}</span>
                <span>{problem.codes.map((code) => codeText(t, code)).join(", ")}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    );
  } else if (!plan) {
    body = <EmptyState icon={RiFileUploadLine}>{t("devtools.import.empty")}</EmptyState>;
  } else {
    body = (
      <>
        {apply.error && (
          <div className="pane-notice" data-tone="danger" role="alert">
            <strong>{t("devtools.import.failed")}</strong>
            <p>{errorText(t, apply.error)}</p>
          </div>
        )}
        {stale && <div className="pane-notice" role="status">{t("devtools.import.stale")}</div>}
        {danger && (
          <div className="pane-notice" data-tone="danger" role="alert">
            <strong>{t("schema.dangerTitle")}</strong>
            <p>{t("schema.dangerText")}</p>
          </div>
        )}
        {sections.length === 0 && <p className="dt-muted">{t("schema.planEmpty")}</p>}
        {sections.map((section) => (
          <section key={section.name} className="schema-plan">
            <h3 className="schema-plan-title mono">{section.name}</h3>
            {section.items.length > 0 ? (
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
            ) : (
              <p className="dt-muted">{t("devtools.import.metadataOnly")}</p>
            )}
            {section.sql.length > 0 && (
              <details className="schema-sql" open={section.danger}>
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
    <div className="dt-transfer">
      <section className="dt-main dt-import-input" aria-label={t("devtools.import.document")}>
        <div className="toolbar">
          <label className="btn btn-secondary btn-sm">
            <RiFileUploadLine size={16} aria-hidden />
            {t("devtools.import.load")}
            <input type="file" accept="application/json,.json" className="visually-hidden" onChange={loadFile} />
          </label>
          <Checkbox label={t("devtools.import.deleteMissing")} checked={deleteMissing} onChange={(event) => setDeleteMissing(event.target.checked)} />
          <div className="dt-actions">
            <Button size="sm" variant="secondary" disabled={!text.trim()} loading={preview.isPending} onClick={runPreview}>{t("devtools.import.preview")}</Button>
          </div>
        </div>
        <textarea
          className="dt-code dt-editor mono"
          aria-label={t("devtools.import.document")}
          placeholder={t("devtools.import.placeholder")}
          spellCheck={false}
          value={text}
          onChange={(event) => setText(event.target.value)}
        />
      </section>
      <section className="dt-plan" aria-label={t("devtools.import.plan")}>
        <header className="pane-header">
          <h2 className="pane-title">{t("devtools.import.plan")}</h2>
        </header>
        <div className="pane-body">{body}</div>
        <footer className="pane-footer">
          <Button
            variant={danger ? "danger" : "primary"}
            loading={apply.isPending}
            disabled={!plan || stale || sections.length === 0 || pending !== null}
            onClick={() => plan && apply.mutate({ identity: newIdentity(), document: plan.document, baseVersion: plan.baseVersion, checksum: plan.checksum })}
          >
            {t("schema.apply")}
          </Button>
        </footer>
      </section>
    </div>
  );
}
