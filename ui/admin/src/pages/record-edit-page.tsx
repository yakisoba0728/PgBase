import { RichTextField } from "./rich-text-field";
import { GeoPointField } from "./geo-point-field";
import { useEffect, useRef, useState } from "react";
import type { FieldDefinition, FileInfo } from "../backend/ports";
import { useBackend } from "../auth/session";
import { RelationTargetPicker } from "../relation/relation-target-picker";
import type { AdminRoute } from "../routing/admin-location";
import { useRecordEditor, type RecordEditor } from "../workspace/record-workspace";
import type { FieldState } from "../workspace/record-codec";

function jsonSelections(value: string): string[] { try { const parsed = JSON.parse(value); return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === "string") : []; } catch { return []; } }

function datetimeLocal(value: unknown): string {
  const date = new Date(String(value));
  if (Number.isNaN(date.getTime())) return "";
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function RecordField({ editor, field }: { editor: RecordEditor; field: FieldDefinition }) {
  const state = editor.stateOf(field);
  const relation = field.type === "relation" ? field.relation : undefined;
  const multipleRelation = relation?.cardinality === "multiple";
  const relationHintID = relation === undefined ? undefined : `record-${field.name}-hint`;
	const helpID = field.help ? `record-${field.name}-help` : undefined;
  const error = editor.errors[field.name];
  const errorID = error ? `record-${field.name}-error` : undefined;
  const describedBy = [helpID, relationHintID, errorID].filter(Boolean).join(" ") || undefined;
  const target = editor.targetOf(field);
  const allowed = field.options?.allowedValues ?? [];

  if (field.type === "autodate") return <div className="record-field"><label htmlFor={`record-${field.name}`}>{field.name}</label><input id={`record-${field.name}`} readOnly value={state.kind === "value" ? String(state.value) : ""} /><p>Set by the server{field.options?.onCreate ? " on creation" : ""}{field.options?.onUpdate ? " on update" : ""}.</p>{field.help && <p>{field.help}</p>}</div>;
  const fixed = ["editor", "geoPoint", "password"].includes(field.type);
  return <div className="record-field">
		{field.help && <p id={helpID}>{field.help}</p>}
    <label htmlFor={`record-${field.name}-state`}>{field.name} state</label>
    <select aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}-state`} onChange={(event) => editor.setKind(field, event.target.value as FieldState["kind"])} value={state.kind}>
      {(editor.isCreate || fixed) && <option value="unset">{editor.isCreate ? "Use default / omit" : "Keep stored value"}</option>}
      {!fixed && !multipleRelation && (field.type !== "relation" || (relation?.minSelect ?? 0) === 0 || state.kind === "null") && <option value="null">NULL</option>}
      <option value="value">Value</option>
    </select>
    {relation !== undefined && <p id={relationHintID}>
      Target: {target?.name ?? "unknown collection"}. {multipleRelation
        ? `Enter one canonical UUID per line; order is preserved (${relation.minSelect}–${relation.maxSelect} selections).`
        : "Enter one canonical UUID."}
    </p>}
    {state.kind === "value" && <>
      <label htmlFor={`record-${field.name}`}>{field.name}</label>
      {field.type === "editor" ? <RichTextField id={`record-${field.name}`} label={field.name} value={String(state.value)} disabled={editor.pending || editor.stale} describedBy={describedBy} invalid={Boolean(error)} convertURLs={field.options?.convertURLs} onChange={value => editor.setValue(field, value)} /> :
        field.type === "geoPoint" ? <GeoPointField id={`record-${field.name}`} label={field.name} value={String(state.value)} disabled={editor.pending || editor.stale} describedBy={describedBy} invalid={Boolean(error)} onChange={value => editor.setValue(field, value)} /> :
        field.type === "boolean" ? <input checked={state.value === true} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.checked)} type="checkbox" /> :
        field.type === "text" && allowed.length > 0 ? <select aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.value)} value={String(state.value)}><option disabled value="">Choose a value</option>{allowed.map((value) => <option key={value}>{value}</option>)}</select> :
        field.type === "jsonb" && allowed.length > 0 ? <select aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} multiple onChange={(event) => editor.setValue(field, JSON.stringify(Array.from(event.target.selectedOptions, (option) => option.value)))} value={jsonSelections(String(state.value))}>{allowed.map((value) => <option key={value}>{value}</option>)}</select> :
        field.type === "jsonb" || multipleRelation ? <textarea aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.value)} rows={multipleRelation ? Math.min(8, Math.max(3, relation?.maxSelect ?? 3)) : 5} value={String(state.value)} /> :
          field.type === "timestamptz" ? <><input aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.value === "" ? "" : new Date(event.target.value).toISOString())} type="datetime-local" value={datetimeLocal(state.value)} /><p>Your local timezone: {Intl.DateTimeFormat().resolvedOptions().timeZone}. Saved as RFC3339 UTC.</p></> :
          <input type={field.type === "password" ? "password" : "text"} autoComplete={field.type === "password" ? "new-password" : undefined} aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} inputMode={["integer", "bigint", "numeric"].includes(field.type) ? "decimal" : undefined} maxLength={field.type === "text" && (field.indexed || field.unique) ? 2048 : undefined} onChange={(event) => editor.setValue(field, event.target.value)} value={String(state.value)} />}
      {relation !== undefined && target !== undefined && <RelationTargetPicker
        disabled={editor.pending || editor.stale}
        draft={String(state.value)}
        field={field}
        key={`${field.name}:${relation.targetCollectionId}`}
        onChange={(value) => editor.setValue(field, value)}
        target={target}
      />}
    </>}
    {field.type === "password" && <p>Stored passwords are never shown, copied, or saved in draft recovery. {field.required ? "Enter a password when creating a record." : "Choose Value and leave it empty to clear the password."}</p>}
    {error && <p className="field-error" id={`record-${field.name}-error`}>{error}</p>}
  </div>;
}

export function FileField({ collection, recordID, field }: { collection: string; recordID: string; field: FieldDefinition }) {
  const { files } = useBackend();
  const [items, setItems] = useState<FileInfo[]>([]);
  const [message, setMessage] = useState("");
  const [pending, setPending] = useState(false);
  const [preview, setPreview] = useState<{ name: string; url: string } | null>(null);
  const input = useRef<HTMLInputElement>(null);
	const lifetime = useRef(0);
	const maximum = field.options?.maxSelect ?? 1;
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
  async function download(item: FileInfo, variant = "original") {
	try { const token = await files.createDownloadToken(item.id, variant); const blob = await files.downloadBlob(item.id, token, variant); const url = URL.createObjectURL(blob); const link = document.createElement("a"); link.href = url; link.download = variant === "original" ? item.filename : `${variant}-${item.filename}`; link.click(); URL.revokeObjectURL(url); }
    catch { setMessage("Download failed."); }
  }
	async function showPreview(item: FileInfo) {
		try { const token = await files.createDownloadToken(item.id, "original"); const blob = await files.downloadBlob(item.id, token); setPreview((current) => { if (current) URL.revokeObjectURL(current.url); return { name: item.filename, url: URL.createObjectURL(blob) }; }); }
		catch { setMessage("Preview failed. You may not have permission to read this protected file."); }
	}
	return <fieldset className="record-field"><legend>{field.name} files</legend>
		{field.required && <p>At least one file is required. Select replacement files above to replace the final file.</p>}
		<p>Preview and download mint a short-lived, file-scoped token. They never expose a protected file through a public URL.</p>
		{message && <p role="alert">{message}</p>}<input aria-label={`${field.name} upload`} ref={input} type="file" disabled={pending || items.length >= maximum} /><button type="button" onClick={upload} disabled={pending || items.length >= maximum}>Upload</button>
		<ul>{items.map((item) => <li key={item.id}>{item.filename} ({item.size} bytes) <button disabled={pending} type="button" onClick={() => void download(item)}>Download</button>{item.contentType.startsWith("image/") && <button disabled={pending} type="button" onClick={() => void showPreview(item)}>Preview</button>}{item.contentType.startsWith("image/") && field.options?.thumbs?.map((variant) => <button disabled={pending} key={variant} type="button" onClick={() => void download(item, variant)}>Thumbnail {variant}</button>)}<button disabled={pending || (field.required && items.length <= 1)} type="button" onClick={() => { const generation = lifetime.current; setPending(true); void files.deleteFile(item.id).then(() => { if (generation === lifetime.current) setItems((current) => current.filter((value) => value.id !== item.id)); }).catch(() => { if (generation === lifetime.current) setMessage("Delete failed."); }).finally(() => { if (generation === lifetime.current) setPending(false); }); }}>Delete</button></li>)}</ul>
		{preview && <div role="dialog" aria-label={`Preview ${preview.name}`}><img alt={preview.name} src={preview.url} /><button type="button" onClick={() => { URL.revokeObjectURL(preview.url); setPreview(null); }}>Close preview</button></div>}
  </fieldset>;
}

function AtomicFileInput({ field, files, error, disabled, onChange }: { field: FieldDefinition; files: File[]; error?: string; disabled: boolean; onChange(files: File[]): void }) {
	const maximum = field.options?.maxSelect ?? 1;
	const [previews, setPreviews] = useState<Array<{ name: string; type: string; url: string }>>([]);
	useEffect(() => { const next = files.filter(file => file.type.startsWith("image/") || file.type.startsWith("video/") || file.type.startsWith("audio/")).map(file => ({ name: file.name, type: file.type, url: URL.createObjectURL(file) })); setPreviews(next); return () => next.forEach(preview => URL.revokeObjectURL(preview.url)); }, [files]);
	return <fieldset className="record-field">
		<legend>{field.name} replacement files on save</legend>
		<input accept={field.options?.mimeTypes?.join(",")} aria-describedby={error ? `record-${field.name}-file-error` : undefined} aria-invalid={error ? "true" : undefined} aria-label={`${field.name} files for save`} disabled={disabled} multiple={maximum > 1} onChange={(event) => onChange(Array.from(event.target.files ?? []))} type="file" />
		<p>On an existing record, selecting files replaces the current set when you save. Select {maximum === 1 ? "one file" : `up to ${maximum} files`}. File selections stay only in this page and must be selected again after a reload.</p>
		{files.length > 0 && <ul>{files.map((file, index) => <li key={`${file.name}:${index}`}>{file.name}</li>)}</ul>}
		{previews.length > 0 && <div aria-label={`${field.name} selected file previews`}>{previews.map(preview => preview.type.startsWith("image/") ? <img alt={`Selected ${preview.name}`} key={preview.url} src={preview.url} /> : preview.type.startsWith("video/") ? <video aria-label={`Selected ${preview.name}`} controls key={preview.url} src={preview.url} /> : <audio aria-label={`Selected ${preview.name}`} controls key={preview.url} src={preview.url} />)}</div>}
		{error && <p className="field-error" id={`record-${field.name}-file-error`}>{error}</p>}
	</fieldset>;
}

export function RecordEditPage({ collectionName, id, navigate }: { collectionName: string; id?: string; navigate(route: AdminRoute): void }) {
	const editor = useRecordEditor(collectionName, id, navigate);
	const [files, setFiles] = useState<Record<string, File[]>>({});
	const [fileErrors, setFileErrors] = useState<Record<string, string>>({});
	useEffect(() => { setFiles({}); setFileErrors({}); }, [collectionName, id]);
	function save() {
		const errors: Record<string, string> = {};
		for (const field of editor.fields.filter((candidate) => candidate.type === "file")) {
			const selected = files[field.name] ?? [];
			const minimum = field.required ? 1 : 0;
			const maximum = field.options?.maxSelect ?? 1;
			if (editor.isCreate && selected.length < minimum) errors[field.name] = `Select at least ${minimum} file${minimum === 1 ? "" : "s"}.`;
			else if (selected.length > maximum) errors[field.name] = `Select no more than ${maximum} file${maximum === 1 ? "" : "s"}.`;
		}
		setFileErrors(errors);
		if (Object.keys(errors).length === 0) editor.save(files);
	}

	return <section>
    <h2>{editor.isCreate ? "Create" : "Edit"} {collectionName} record</h2>
    {editor.message && <p role="alert">{editor.message}</p>}
    {editor.stale && <button onClick={editor.reload} type="button">Reload record</button>}
    {!editor.collection && !editor.message && <p aria-live="polite">Loading record…</p>}
		{editor.collection && <form onSubmit={(event) => { event.preventDefault(); save(); }}>
			{editor.fields.map((field) => field.type === "file" ? <div key={field.name}>
				<AtomicFileInput disabled={editor.pending || editor.stale} error={fileErrors[field.name] ?? editor.errors[field.name]} field={field} files={files[field.name] ?? []} onChange={(selected) => { setFiles((current) => ({ ...current, [field.name]: selected })); setFileErrors((current) => { const next = { ...current }; delete next[field.name]; return next; }); }} />
				{id && <FileField collection={collectionName} recordID={id} field={field} />}
			</div> : <RecordField editor={editor} field={field} key={field.name} />)}
      <button disabled={editor.pending || editor.stale} type="submit">{editor.pending ? "Saving…" : editor.isCreate ? "Create record" : "Save changes"}</button>
      {!editor.isCreate && <button disabled={editor.pending} onClick={editor.duplicate} type="button">Duplicate record</button>}
      {!editor.isCreate && <button className="danger" disabled={editor.pending || editor.stale} onClick={editor.remove} type="button">Delete record</button>}
    </form>}
  </section>;
}
