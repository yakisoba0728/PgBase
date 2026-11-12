import { t, useLocale, message as bilingualMessage, type Message } from "../i18n";
import { useEffect, useRef, useState } from "react";
import { useBackend, useSession } from "../auth/session";
import type { SchemaDocument, SchemaImportPlan, SchemaImportResult } from "../backend/ports";
import { parseJSON, stringifyJSON } from "../backend/json";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import { Button, PageHeading } from "../components/ui";

type ImportOutcome = SchemaImportResult;
type Recovery = { version: 1; administratorId: string; operationId: string; idempotencyKey: string; baseVersion: number; checksum: string; policyChecksum: string; documentDigest: string };
const legacyRecoveryKey = "pgbase.schemaImportOperation";
const recoveryKey = (administratorId: string) => `pgbase.schemaImportOperation.v1.${administratorId}`;

export function schemaDocumentDigest(document: SchemaDocument): Promise<string> {
  return crypto.subtle.digest("SHA-256", new TextEncoder().encode(stringifyJSON(document))).then((value) =>
    Array.from(new Uint8Array(value), (byte) => byte.toString(16).padStart(2, "0")).join(""));
}

function readRecovery(administratorId: string): Recovery | undefined {
  const key = recoveryKey(administratorId);
  try {
    sessionStorage.removeItem(legacyRecoveryKey);
    const parsed = JSON.parse(sessionStorage.getItem(key) ?? "null") as Partial<Recovery> | null;
    const bounded = (value: unknown, maximum: number) => typeof value === "string" && value.length > 0 && value.length <= maximum;
    if (!parsed || parsed.version !== 1 || parsed.administratorId !== administratorId || !bounded(parsed.operationId, 128) || !bounded(parsed.idempotencyKey, 128) || !Number.isSafeInteger(parsed.baseVersion) || (parsed.baseVersion ?? -1) < 0 || !bounded(parsed.checksum, 256) || !bounded(parsed.policyChecksum, 256) || !bounded(parsed.documentDigest, 64)) {
      sessionStorage.removeItem(key); return undefined;
    }
    return parsed as Recovery;
  } catch { return undefined; }
}

export function SchemaTransferPage() {
  useLocale();
  const { schema } = useBackend();
  const { state } = useSession();
  const administratorId = state.kind === "authenticated" ? state.administrator.id : "";
  const [text, setText] = useState("");
  const [documentDirty, setDocumentDirty] = useState(false);
  useUnsavedDraft("schema-import", documentDirty);
  const [plan, setPlan] = useState<SchemaImportPlan>();
  const [recovery, setRecovery] = useState<Recovery>();
  const [message, setMessage] = useState<Message>();
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);

  const clearRecovery = () => {
    try { if (administratorId) sessionStorage.removeItem(recoveryKey(administratorId)); } catch { /* terminal server state is already confirmed */ }
    setRecovery(undefined);
  };
  const showOutcome = (result: ImportOutcome) => {
    if (result.status === "committed") {
      clearRecovery(); setPlan(undefined);
      setMessage(bilingualMessage(`Schema import committed${result.version === undefined ? "" : ` at version ${result.version}`}${result.applied === undefined ? "." : ` with ${result.applied} changes.`}`, `스키마 가져오기를 완료했습니다${result.version === undefined ? "" : ` (버전 ${result.version})`}${result.applied === undefined ? "." : `. 변경 ${result.applied}개를 적용했습니다.`}`));
    } else if (result.status === "failed") {
      clearRecovery(); setPlan(undefined); setMessage(bilingualMessage(`Schema import ${result.operationId} failed. Review the current schema before creating a new preview.`, `스키마 가져오기 ${result.operationId}에 실패했습니다. 새 미리보기를 만들기 전에 현재 스키마를 확인하세요.`));
    } else if (result.status === "unknown") {
      setMessage(bilingualMessage(`The server cannot confirm operation ${result.operationId}. Retry the exact request; after a reload, paste the same document to restore that retry.`, `서버에서 작업 ${result.operationId}의 결과를 확인할 수 없습니다. 같은 요청으로 재시도하세요. 새로고침했다면 동일한 문서를 붙여넣어 재시도 요청을 복구하세요.`));
    } else setMessage(bilingualMessage(`Schema import ${result.operationId} is still running. Check its status before retrying.`, `스키마 가져오기 ${result.operationId}이 진행 중입니다. 재시도하기 전에 상태를 확인하세요.`));
  };

  async function checkStatus(item = recovery) {
    if (!item || !schema.importStatus) return;
    const issued = ++generation.current; setBusy(true);
    try { const result = await schema.importStatus(item.operationId) as ImportOutcome; if (issued === generation.current) showOutcome(result); }
    catch { if (issued === generation.current) setMessage(bilingualMessage(`Unable to check operation ${item.operationId}. Its recovery information is retained.`, `작업 ${item.operationId}의 상태를 확인하지 못했습니다. 복구 정보는 유지됩니다.`)); }
    finally { if (issued === generation.current) setBusy(false); }
  }

  useEffect(() => {
    generation.current += 1;
    if (!administratorId) return;
    const item = readRecovery(administratorId);
    setRecovery(item);
    if (item && schema.importStatus) void checkStatus(item);
    return () => { generation.current += 1; };
    // schema identity and administrator identity delimit recovery ownership.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [schema, administratorId]);

  async function exportSchema() {
    if (!schema.exportDocument) return;
    const issued = ++generation.current; setBusy(true); setMessage(undefined);
    try {
      const schemaDocument = await schema.exportDocument(); if (issued !== generation.current) return;
      const content = stringifyJSON(schemaDocument);
      const link = document.createElement("a"); link.href = URL.createObjectURL(new Blob([content], { type: "application/json" }));
      link.download = "pgbase-schema.json"; link.click(); URL.revokeObjectURL(link.href);
    } catch { if (issued === generation.current) setMessage("Unable to export the schema."); }
    finally { if (issued === generation.current) setBusy(false); }
  }

  async function preview() {
    if (!schema.previewImport || recovery) return;
    const issued = ++generation.current; setBusy(true); setMessage(undefined); setPlan(undefined);
    try { const document = parseJSON(text) as SchemaDocument; const next = await schema.previewImport(document); if (issued === generation.current) setPlan(next); }
    catch { if (issued === generation.current) setMessage("The schema document is invalid or could not be previewed."); }
    finally { if (issued === generation.current) setBusy(false); }
  }

  async function restoreExactRetry() {
    if (!recovery) return;
    const issued = ++generation.current; setBusy(true); setMessage(undefined);
    try {
      const document = parseJSON(text) as SchemaDocument;
      if (await schemaDocumentDigest(document) !== recovery.documentDigest) throw new Error("different document");
      if (issued === generation.current) {
        setPlan({ document, changes: [], baseVersion: recovery.baseVersion, checksum: recovery.checksum, policyChecksum: recovery.policyChecksum, operationId: recovery.operationId, idempotencyKey: recovery.idempotencyKey });
        setMessage(bilingualMessage(`Recovered the exact request for operation ${recovery.operationId}.`, `작업 ${recovery.operationId}의 동일 요청을 복구했습니다.`));
      }
    } catch { if (issued === generation.current) setMessage("This document does not match the pending import. Paste the exact original document."); }
    finally { if (issued === generation.current) setBusy(false); }
  }

  async function apply() {
    if (!plan || !schema.applyImport || !administratorId || !plan.operationId || !plan.idempotencyKey) return;
    const issued = ++generation.current; setBusy(true); setMessage(undefined);
    let item: Recovery | undefined;
    let sent = false;
    try {
      if (recovery && recovery.operationId !== plan.operationId) throw new Error("another import is unresolved");
      item = { version: 1, administratorId, operationId: plan.operationId, idempotencyKey: plan.idempotencyKey, baseVersion: plan.baseVersion, checksum: plan.checksum, policyChecksum: plan.policyChecksum, documentDigest: await schemaDocumentDigest(plan.document) };
      if (issued !== generation.current) return;
      sessionStorage.setItem(recoveryKey(administratorId), JSON.stringify(item)); setRecovery(item);
      sent = true;
      const result = await schema.applyImport(plan) as ImportOutcome;
      if (issued === generation.current) {
        showOutcome(result);
        if (result.status === "committed") setDocumentDirty(false);
      }
    }
    catch { if (issued === generation.current) setMessage(sent && item ? bilingualMessage(`The response for operation ${item.operationId} was not confirmed. Check status or retry this exact request.`, `작업 ${item.operationId}의 응답을 확인하지 못했습니다. 상태를 확인하거나 같은 요청으로 재시도하세요.`) : "Unable to save the import recovery information. The import was not sent."); }
    finally { if (issued === generation.current) setBusy(false); }
  }

  function changeDocument(value: string) {
	 generation.current += 1;
	 setBusy(false); setText(value); setDocumentDirty(value.trim() !== ""); setPlan(undefined);
  }

  async function readDocument(file: File | undefined) {
    if (!file) return;
    if (file.size > 16 * 1024 * 1024) { setMessage("Schema files must be no larger than 16 MiB."); return; }
    const issued = ++generation.current; setBusy(true); setMessage(undefined);
    try {
      const content = await file.text();
      if (issued !== generation.current) return;
      setText(content); setPlan(undefined); setDocumentDirty(content.trim() !== "");
    } catch { if (issued === generation.current) setMessage("Unable to read the schema file. Your current document is preserved."); }
    finally { if (issued === generation.current) setBusy(false); }
  }

  const heading = <PageHeading title={t("Schema transfer")} description={t("Export your collection definitions or review a schema before importing it.")} />;
  if (!schema.exportDocument || !schema.previewImport || !schema.applyImport) return <section className="schema-transfer">{heading}<p>{t("This server does not support schema transfer.")}</p></section>;
  return <section className="schema-transfer">
    {heading}
    <section aria-labelledby="schema-export-title" className="schema-export">
      <div><h3 id="schema-export-title">{t("Export schema")}</h3><p>{t("Download the current schema as a JSON file.")}</p></div>
      <Button disabled={busy} onClick={() => void exportSchema()} variant="secondary">{t("Export schema")}</Button>
    </section>
    <section aria-labelledby="schema-import-title" className="schema-import">
      <div className="schema-section-heading"><h3 id="schema-import-title">{t("Import schema")}</h3><p>{t("Choose a JSON file or paste a schema document. Preview the changes before applying them.")}</p></div>
      <label className="schema-file-input">{t("Import JSON file")}<input type="file" accept=".json,application/json" disabled={busy} onChange={event => { void readDocument(event.target.files?.[0]); event.target.value = ""; }} /></label>
      <div className="schema-editor-label"><label htmlFor="schema-document">{t("Schema document")}</label><span>JSON</span></div>
      <textarea aria-describedby="schema-import-help" autoCapitalize="none" autoComplete="off" className="schema-document" id="schema-document" onChange={(event) => changeDocument(event.target.value)} placeholder={t("Paste your schema JSON here\u2026")} rows={12} spellCheck={false} value={text} />
      <div className="schema-import-actions">
        <p id="schema-import-help">{t("Collections, fields, and indexes omitted from the document are preserved.")}</p>
        <Button disabled={busy || !!recovery || text.trim() === ""} onClick={() => void preview()}>{t("Preview import")}</Button>
      </div>
    </section>
    {recovery && <section aria-label={t("Import recovery")} className="schema-recovery">
      <h3>{t("Pending import")}</h3>
      <p>{t("Check the operation status, or paste the original document to recover the exact request.")}</p>
      <div className="schema-result-actions"><Button disabled={busy || !schema.importStatus} onClick={() => void checkStatus()} variant="secondary">{t("Check import status")}</Button><Button disabled={busy || text.trim() === ""} onClick={() => void restoreExactRetry()} variant="secondary">{t("Recover exact retry")}</Button></div>
    </section>}
    {plan && <section aria-label={t("Import preview")} className="schema-preview">
      <h3>{t("Import preview")}</h3>
      <p>{t("{count} schema changes from version {version}.", { count: plan.changes.length, version: plan.baseVersion })}</p>
      <ol>{plan.document.collections.map((collection) => <li key={collection.name}><strong>{collection.name}</strong>: {collection.view ? t("View") : t("{count} supplied fields", { count: collection.fields?.length ?? 0 })}; {t("user access {state}", { state: collection.userAccess ? t("enabled") : t("disabled") })}; {t("{count} rule actions", { count: Object.keys(collection.rules).length })}</li>)}</ol>
      <Button disabled={busy} onClick={() => void apply()}>{recovery && plan.operationId === recovery.operationId ? t("Retry exact import") : t("Apply import")}</Button>
    </section>}
    {message && <p className="schema-message" role="status">{t(message)}</p>}
  </section>;
}
