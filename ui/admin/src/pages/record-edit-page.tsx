import { useEffect, useRef, useState } from "react";
import type { FieldDefinition, FileInfo } from "../backend/ports";
import { useBackend } from "../auth/session";
import { RelationTargetPicker } from "../relation/relation-target-picker";
import type { AdminRoute } from "../routing/admin-location";
import { useRecordEditor, type RecordEditor } from "../workspace/record-workspace";
import type { FieldState } from "../workspace/record-codec";

function jsonSelections(value: string): string[] { try { const parsed = JSON.parse(value); return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : []; } catch { return []; } }

function RecordField({ editor, field }: { editor: RecordEditor; field: FieldDefinition }) {
  const state = editor.stateOf(field);
  const relation = field.type === "relation" ? field.relation : undefined;
  const multipleRelation = relation?.cardinality === "multiple";
  const relationHintID = relation === undefined ? undefined : `record-${field.name}-hint`;
  const error = editor.errors[field.name];
  const errorID = error ? `record-${field.name}-error` : undefined;
  const describedBy = [relationHintID, errorID].filter(Boolean).join(" ") || undefined;
  const target = editor.targetOf(field);
  const allowed = field.options?.allowedValues ?? [];

  return <div className="record-field">
    <label htmlFor={`record-${field.name}-state`}>{field.name} state</label>
    <select aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}-state`} onChange={(event) => editor.setKind(field, event.target.value as FieldState["kind"])} value={state.kind}>
      {editor.isCreate && <option value="unset">Use default / omit</option>}
      {!multipleRelation && (field.type !== "relation" || (relation?.minSelect ?? 0) === 0 || state.kind === "null") && <option value="null">NULL</option>}
      <option value="value">Value</option>
    </select>
    {relation !== undefined && <p id={relationHintID}>
      Target: {target?.name ?? "unknown collection"}. {multipleRelation
        ? `Enter one canonical UUID per line; order is preserved (${relation.minSelect}–${relation.maxSelect} selections).`
        : "Enter one canonical UUID."}
    </p>}
    {state.kind === "value" && <>
      <label htmlFor={`record-${field.name}`}>{field.name}</label>
      {field.type === "boolean" ? <input checked={state.value === true} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.checked)} type="checkbox" /> :
        field.type === "text" && allowed.length > 0 ? <select aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.value)} value={String(state.value)}><option disabled value="">Choose a value</option>{allowed.map((value) => <option key={value}>{value}</option>)}</select> :
        field.type === "jsonb" && allowed.length > 0 ? <select aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} multiple onChange={(event) => editor.setValue(field, JSON.stringify(Array.from(event.target.selectedOptions, (option) => option.value)))} value={jsonSelections(String(state.value))}>{allowed.map((value) => <option key={value}>{value}</option>)}</select> :
        field.type === "jsonb" || multipleRelation ? <textarea aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.value)} rows={multipleRelation ? Math.min(8, Math.max(3, relation?.maxSelect ?? 3)) : 5} value={String(state.value)} /> :
          <input aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} inputMode={["integer", "bigint", "numeric"].includes(field.type) ? "decimal" : undefined} maxLength={field.type === "text" && (field.indexed || field.unique) ? 2048 : undefined} onChange={(event) => editor.setValue(field, event.target.value)} value={String(state.value)} />}
      {relation !== undefined && target !== undefined && <RelationTargetPicker
        disabled={editor.pending || editor.stale}
        draft={String(state.value)}
        field={field}
        key={`${field.name}:${relation.targetCollectionId}`}
        onChange={(value) => editor.setValue(field, value)}
        target={target}
      />}
    </>}
    {error && <p className="field-error" id={`record-${field.name}-error`}>{error}</p>}
  </div>;
}

export function FileField({ collection, recordID, field }: { collection: string; recordID: string; field: FieldDefinition }) {
  const { files } = useBackend();
  const [items, setItems] = useState<FileInfo[]>([]);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const lifetime = useRef(0);
  useEffect(() => {
    lifetime.current += 1;
    const controller = new AbortController();
    void files.listFiles(collection, recordID, field.name, controller.signal).then((value) => {
      if (!controller.signal.aborted) setItems(value);
    }).catch(() => { if (!controller.signal.aborted) setMessage("Unable to load files."); });
    return () => controller.abort();
  }, [collection, field.name, files, recordID]);
  async function upload() {
    const file = input.current?.files?.[0]; if (!file || pending) return;
    const generation = lifetime.current; setPending(true); setMessage("");
    try { const uploaded = await files.uploadFile(collection, recordID, field.name, file); if (generation === lifetime.current) { setItems((current) => [...current, uploaded]); if (input.current) input.current.value = ""; } }
    catch { if (generation === lifetime.current) setMessage("Upload failed."); } finally { if (generation === lifetime.current) setPending(false); }
  }
  async function download(item: FileInfo) {
    try { const token = await files.createDownloadToken(item.id); const blob = await files.downloadBlob(item.id, token); const url = URL.createObjectURL(blob); const link = document.createElement("a"); link.href = url; link.download = item.filename; link.click(); URL.revokeObjectURL(url); }
    catch { setMessage("Download failed."); }
  }
  return <fieldset className="record-field"><legend>{field.name} files</legend>
    {message && <p role="alert">{message}</p>}<input aria-label={`${field.name} upload`} ref={input} type="file" disabled={pending} /><button type="button" onClick={upload} disabled={pending}>Upload</button>
    <ul>{items.map((item) => <li key={item.id}>{item.filename} ({item.size} bytes) <button disabled={pending} type="button" onClick={() => void download(item)}>Download</button><button disabled={pending} type="button" onClick={() => { const generation = lifetime.current; setPending(true); void files.deleteFile(item.id).then(() => { if (generation === lifetime.current) setItems((current) => current.filter((value) => value.id !== item.id)); }).catch(() => { if (generation === lifetime.current) setMessage("Delete failed."); }).finally(() => { if (generation === lifetime.current) setPending(false); }); }}>Delete</button></li>)}</ul>
  </fieldset>;
}

export function RecordEditPage({ collectionName, id, navigate }: { collectionName: string; id?: string; navigate(route: AdminRoute): void }) {
  const editor = useRecordEditor(collectionName, id, navigate);

  return <section>
    <h2>{editor.isCreate ? "Create" : "Edit"} {collectionName} record</h2>
    {editor.message && <p role="alert">{editor.message}</p>}
    {editor.stale && <button onClick={editor.reload} type="button">Reload record</button>}
    {!editor.collection && !editor.message && <p aria-live="polite">Loading record…</p>}
    {editor.collection && <form onSubmit={(event) => { event.preventDefault(); editor.save(); }}>
      {editor.fields.map((field) => field.type === "file" && id ? <FileField collection={collectionName} recordID={id} field={field} key={field.name} /> : <RecordField editor={editor} field={field} key={field.name} />)}
      <button disabled={editor.pending || editor.stale} type="submit">{editor.pending ? "Saving…" : editor.isCreate ? "Create record" : "Save changes"}</button>
      {!editor.isCreate && <button disabled={editor.pending} onClick={editor.duplicate} type="button">Duplicate record</button>}
      {!editor.isCreate && <button className="danger" disabled={editor.pending || editor.stale} onClick={editor.remove} type="button">Delete record</button>}
    </form>}
  </section>;
}
