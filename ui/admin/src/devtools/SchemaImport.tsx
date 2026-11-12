import { useEffect, useState, type ChangeEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiFileUploadLine } from "@remixicon/react";
import { ApiError, parseJson } from "../api/http";
import { previewSchemaImport, type SchemaDocument } from "../api/schema";
import { errorText } from "../app/queries";
import { collectionsQuery } from "../collections/queries";
import { trackOperation, untrackOperation, watchOperation } from "../app/operations";
import { Button } from "../components/Button";
import { ChangeList } from "../components/ChangeList";
import { CodeArea } from "../components/CodeArea";
import { EmptyState, Loader, Notice } from "../components/Feedback";
import { Checkbox } from "../components/Field";
import { toast } from "../components/Toast";
import { useT, type MessageKey, type Translate } from "../i18n";
import { newIdentity, Undecided } from "../schema/apply";
import { dangerousStatement } from "../schema/draft";
import { en } from "../i18n/en";
import { importSections, problemList, readPending, writePending, type PendingImport } from "./model";
import { runImport } from "./queries";
import { PaneHeader } from "../components/SplitView";

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
    mutationFn: async (next: PendingImport) => {
      writePending(next);
      setPending(next);
      trackOperation({ kind: "import", id: next.identity.operationId, checksum: next.checksum });
      const release = watchOperation(next.identity.operationId);
      try {
        return await runImport(next);
      } catch (error) {
        if (!(error instanceof Undecided) && !(error instanceof ApiError && error.code === "network_error")) untrackOperation(next.identity.operationId);
        throw error;
      } finally {
        release();
      }
    },
    onSuccess: (result, next) => {
      untrackOperation(next.identity.operationId);
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
      // A resumed apply after reload has no plan on screen to carry the error.
      toast(`${t("devtools.import.failed")} ${errorText(t, error)}`, "danger");
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
          <Notice tone="danger" title={t("devtools.import.failed")}>{errorText(t, apply.error)}</Notice>
        )}
        {stale && <Notice role="status">{t("devtools.import.stale")}</Notice>}
        {danger && (
          <Notice tone="danger" title={t("schema.dangerTitle")}>{t("schema.dangerText")}</Notice>
        )}
        {sections.length === 0 && <p className="dt-muted">{t("schema.planEmpty")}</p>}
        {sections.map((section) => (
          <ChangeList key={section.name} title={section.name} changes={section.items} sql={section.sql} dangerous={dangerousStatement} emptyText={t("devtools.import.metadataOnly")} />
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
        <CodeArea fill label={t("devtools.import.document")} placeholder={t("devtools.import.placeholder")} value={text} onChange={(event) => setText(event.target.value)} onSubmit={() => text.trim() && runPreview()} />
      </section>
      <section className="dt-plan" aria-label={t("devtools.import.plan")}>
        <PaneHeader title={t("devtools.import.plan")} />
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
