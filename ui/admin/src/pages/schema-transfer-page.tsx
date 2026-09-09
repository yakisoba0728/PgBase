import { useEffect, useRef, useState } from "react";
import { useBackend, useSession } from "../auth/session";
import type { SchemaDocument, SchemaImportPlan, SchemaImportResult } from "../backend/ports";
import { parseJSON, stringifyJSON } from "../backend/json";

type ImportOutcome = SchemaImportResult;
type Recovery = { version: 1; administratorId: string; operationId: string; idempotencyKey: string; baseVersion: number; checksum: string; policyChecksum: string; documentDigest: string };
const legacyRecoveryKey = "pgbase.schemaImportOperation";
const recoveryKey = (administratorId: string) => `pgbase.schemaImportOperation.v1.${administratorId}`;

export function schemaDocumentDigest(document: SchemaDocument): Promise<string> {
  return crypto.subtle.digest("SHA-256", new TextEncoder().encode(stringifyJSON(document))).then((value) =>
    Array.from(new Uint8Array(value), (byte) => byte.toString(16).padStart(2, "0")).join(""));
}

function readRecovery(administratorId: string): Recovery | undefined {
  sessionStorage.removeItem(legacyRecoveryKey);
  const key = recoveryKey(administratorId);
  try {
    const parsed = JSON.parse(sessionStorage.getItem(key) ?? "null") as Partial<Recovery> | null;
    const bounded = (value: unknown, maximum: number) => typeof value === "string" && value.length > 0 && value.length <= maximum;
    if (!parsed || parsed.version !== 1 || parsed.administratorId !== administratorId || !bounded(parsed.operationId, 128) || !bounded(parsed.idempotencyKey, 128) || !Number.isSafeInteger(parsed.baseVersion) || (parsed.baseVersion ?? -1) < 0 || !bounded(parsed.checksum, 256) || !bounded(parsed.policyChecksum, 256) || !bounded(parsed.documentDigest, 64)) {
      sessionStorage.removeItem(key); return undefined;
    }
    return parsed as Recovery;
  } catch { sessionStorage.removeItem(key); return undefined; }
}

export function SchemaTransferPage() {
  const { schema } = useBackend();
  const { state } = useSession();
  const administratorId = state.kind === "authenticated" ? state.administrator.id : "";
  const [text, setText] = useState("");
  const [plan, setPlan] = useState<SchemaImportPlan>();
  const [recovery, setRecovery] = useState<Recovery>();
  const [message, setMessage] = useState<string>();
  const [busy, setBusy] = useState(false);
  const generation = useRef(0);

  const clearRecovery = () => {
    if (administratorId) sessionStorage.removeItem(recoveryKey(administratorId));
    setRecovery(undefined);
  };
  const showOutcome = (result: ImportOutcome) => {
    if (result.status === "committed") {
      clearRecovery(); setPlan(undefined);
      setMessage(`Schema import committed${result.version === undefined ? "" : ` at version ${result.version}`}${result.applied === undefined ? "." : ` with ${result.applied} changes.`}`);
    } else if (result.status === "failed") {
      clearRecovery(); setPlan(undefined); setMessage(`Schema import ${result.operationId} failed. Review the current schema before creating a new preview.`);
    } else if (result.status === "unknown") {
      setMessage(`The server cannot confirm operation ${result.operationId}. Retry the exact request; after a reload, paste the same document to restore that retry.`);
    } else setMessage(`Schema import ${result.operationId} is still running. Check its status before retrying.`);
  };

  async function checkStatus(item = recovery) {
    if (!item || !schema.importStatus) return;
    const issued = ++generation.current; setBusy(true);
    try { const result = await schema.importStatus(item.operationId) as ImportOutcome; if (issued === generation.current) showOutcome(result); }
    catch { if (issued === generation.current) setMessage(`Unable to check operation ${item.operationId}. Its recovery information is retained.`); }
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
      const content = stringifyJSON(schemaDocument); setText(content); setPlan(undefined);
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
        setMessage(`Recovered the exact request for operation ${recovery.operationId}.`);
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
      const result = await schema.applyImport(plan) as ImportOutcome; if (issued === generation.current) showOutcome(result);
    }
    catch { if (issued === generation.current) setMessage(sent && item ? `The response for operation ${item.operationId} was not confirmed. Check status or retry this exact request.` : "Unable to save the import recovery information. The import was not sent."); }
    finally { if (issued === generation.current) setBusy(false); }
  }

  function changeDocument(value: string) {
	 generation.current += 1;
	 setBusy(false); setText(value); setPlan(undefined);
  }

  if (!schema.exportDocument || !schema.previewImport || !schema.applyImport) return <section><h2>Schema import and export</h2><p>This server does not support schema transfer.</p></section>;
  return <section>
    <h2>Schema import and export</h2>
    <p>Export a portable logical schema, or paste one below. Import preserves collections, fields, and indexes omitted from the document.</p>
    <button disabled={busy} onClick={() => void exportSchema()} type="button">Export schema</button>
    <label>Schema document<textarea aria-label="Schema document" onChange={(event) => changeDocument(event.target.value)} rows={18} value={text} /></label>
    <button disabled={busy || !!recovery || text.trim() === ""} onClick={() => void preview()} type="button">Preview import</button>
    {recovery && <div aria-label="Import recovery"><button disabled={busy || !schema.importStatus} onClick={() => void checkStatus()} type="button">Check import status</button><button disabled={busy || text.trim() === ""} onClick={() => void restoreExactRetry()} type="button">Recover exact retry</button></div>}
    {plan && <div aria-label="Import preview">
      <p>{plan.changes.length} schema changes from version {plan.baseVersion}.</p>
      <ol>{plan.document.collections.map((collection) => <li key={collection.name}><strong>{collection.name}</strong>: {collection.view ? "view" : `${collection.fields?.length ?? 0} supplied fields`}; user access {collection.userAccess ? "enabled" : "disabled"}; {Object.keys(collection.rules).length} rule actions</li>)}</ol>
      <button disabled={busy} onClick={() => void apply()} type="button">{recovery && plan.operationId === recovery.operationId ? "Retry exact import" : "Apply import"}</button>
    </div>}
    {message && <p aria-live="polite">{message}</p>}
  </section>;
}
