import { useEffect, useState } from "react";
import { useBackend } from "../auth/session";
import type { AuthCollection, CollectionCatalog, FieldInput, RecordIDPolicy } from "../backend/ports";
import { stringifyJSON } from "../backend/json";
import { applyEdit, buildInput, newField, relationTargets, scaffoldFields } from "../designer/collection-draft";
import { CollectionFieldEditor } from "../pages/collection-field-editor";
import { RecordIDPolicyEditor } from "../pages/record-id-policy-editor";
import { useMutationScope } from "./feature-state";
import { Button } from "./ui";
import { useUnsavedDraft } from "../routing/unsaved-draft";
const ruleNames = ["list", "view", "create", "update", "delete", "auth", "manage"] as const;
export function AuthCollectionSchema({ collection, authCollections = [], onSaved, onDirty, disabled = false }: { authCollections?: AuthCollection[]; disabled?: boolean; onDirty?(id: string, dirty: boolean): void; collection: AuthCollection; onSaved(value: AuthCollection): void }) {
  const [open, setOpen] = useState(false);
  return <details className="auth-administration" onToggle={e => e.currentTarget.open && setOpen(true)}><summary>Fields, access rules and record identity</summary>{open && <SchemaEditor key={stringifyJSON(collection)} collection={collection} authCollections={authCollections} onSaved={onSaved} onDirty={onDirty} disabled={disabled} />}</details>;
}
function SchemaEditor({ collection, authCollections = [], onSaved, onDirty, disabled = false }: { authCollections?: AuthCollection[]; disabled?: boolean; onDirty?(id: string, dirty: boolean): void; collection: AuthCollection; onSaved(value: AuthCollection): void }) {
  const { authCollections: port, schema } = useBackend();
  const initial = (collection.fields ?? []).filter(field => !field.system);
  const [fields, setFields] = useState<import("../designer/collection-draft").DraftField[]>(() => initial.length ? scaffoldFields(initial) : []);
  const [rules, setRules] = useState(collection.rules ?? {});
  const [recordId, setRecordID] = useState<RecordIDPolicy | undefined>(collection.recordId);
  const [catalog, setCatalog] = useState<CollectionCatalog | null>(null);
  const mutation = useMutationScope();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [confirmRemove, setConfirmRemove] = useState(false);
  const signature = stringifyJSON({ fields, rules, recordId });
  const [pristine] = useState(signature);
  useEffect(() => { onDirty?.(collection.id, signature !== pristine); return () => onDirty?.(collection.id, false); }, [onDirty, collection.id, signature, pristine]);
  const release = useUnsavedDraft(`auth-schema:${collection.id}`, signature !== pristine);
  useEffect(() => { const c = new AbortController(); void schema.catalog(c.signal).then(v => { if (!c.signal.aborted) setCatalog(v); }).catch(() => { if (!c.signal.aborted) setMessage("Relation targets could not be loaded. Existing fields are preserved."); }); return () => c.abort(); }, [schema]);
  const targetNames = new Set([...relationTargets(catalog, collection.name).map(target => target.name), ...authCollections.map(item => item.name), ...fields.flatMap(field => field.relation?.targetCollection ? [field.relation.targetCollection] : [])]);
  const targets = [...targetNames].sort((a, b) => a.localeCompare(b)).map(name => ({ name, label: name === collection.name ? `${name} (self)` : name }));
  const removed = initial.filter(field => !fields.some(next => field.id !== undefined && next.id === field.id || field.id === undefined && next.name === field.name));
  useEffect(() => setConfirmRemove(false), [removed.map(field => field.name).join("\0")]);
  async function save() {
    if (busy) return;
    if (removed.length && !confirmRemove) { setMessage("Confirm the removal of field definitions before saving."); return; }
    let input: FieldInput[] = [];
    try { if (fields.length) { const change = buildInput("update", collection.name, fields); if ("fields" in change) input = change.fields ?? []; } }
    catch (error) { setMessage(error instanceof Error ? error.message : "Check field definitions."); return; }
    const signal = mutation.begin(); if (!signal) return;
    setBusy(true); setMessage("");
    try { const saved = await port.updateAuthCollection(collection.id, { registrationEnabled: collection.registrationEnabled, profileSchema: collection.profileSchema, fields: input.map(({ id: _id, default: _default, ...field }) => ({ ...field, unique: false })), ...(collection.rules === undefined && Object.keys(rules).length === 0 ? {} : { rules }), }, signal); if (!mutation.current(signal)) return; release(); onSaved(saved); setMessage("Collection schema saved."); }
    catch { if (mutation.current(signal)) setMessage("The schema change could not be confirmed. Existing profiles may conflict with the new definitions. Reload the collection before retrying."); }
    finally { if (mutation.current(signal)) setBusy(false); mutation.end(signal); }
  }
  return <fieldset disabled={busy || disabled} className="plain-fields">
    {disabled && <p role="status">Save or discard the profile JSON draft before changing structured fields.</p>}
    <h4>Custom fields</h4><p className="settings-help">The server checks existing user profiles and rejects incompatible definitions. Renaming or removing a field does not migrate stored profile values.</p>
    {collection.fields === undefined && <p role="status">This server did not return structured field metadata. Reload before editing fields.</p>}
    {fields.map((field, index) => <CollectionFieldEditor authProfile idPrefix={`auth-${collection.id}`} key={index} field={field} index={index} fieldCount={Math.max(fields.length, 2)} pending={busy} relationTargets={targets} relationTargetsLoading={catalog === null} showRequiredRelationMigration={false} onEdit={edit => setFields(current => current.map((value, i) => i === index ? applyEdit(value, edit, collection.name) : value))} onRemove={() => setFields(current => current.filter((_, i) => i !== index))} />)}
    <Button variant="secondary" disabled={collection.fields === undefined} onClick={() => setFields(current => [...current, newField()])}>Add profile field</Button>
    <h4>Access rules</h4><p className="settings-help">Locked rejects client access, public allows it, and an expression limits access. Administrator access is retained.</p>
    <div className="settings-field-grid">{ruleNames.map(name => <div key={name}><label>{name} rule<select value={rules[name] === null ? "locked" : rules[name] === "" ? "public" : rules[name] === undefined ? "unchanged" : "expression"} onChange={e => setRules(current => ({ ...current, [name]: e.target.value === "locked" ? null : e.target.value === "public" ? "" : "@request.auth.id != ''" }))}><option value="unchanged" disabled>Not returned</option><option value="locked">Locked</option><option value="public">Public</option><option value="expression">Expression</option></select></label>{typeof rules[name] === "string" && rules[name] !== "" && <label>{name} expression<input spellCheck={false} value={rules[name] ?? ""} onChange={e => setRules(current => ({ ...current, [name]: e.target.value }))} /></label>}</div>)}</div>
    <RecordIDPolicyEditor value={recordId} locked={true} disabled={busy} onChange={setRecordID} />
    {removed.length > 0 && <label className="danger-zone"><input type="checkbox" checked={confirmRemove} onChange={e => setConfirmRemove(e.target.checked)} />Confirm removal of field definitions: {removed.map(v => v.name).join(", ")}</label>}
    {message && <p role="status">{message}</p>}
    <Button disabled={busy || collection.fields === undefined || removed.length > 0 && !confirmRemove} onClick={() => void save()}>{busy ? "Saving…" : "Save fields and rules"}</Button>
  </fieldset>;
}
