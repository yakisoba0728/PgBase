import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { AuthCollection, AuthRecord, AuthRecordInput, AuthRecordsPort, RecordData } from "../backend/ports";
import { parseJSON, stringifyJSON } from "../backend/json";
import { Button, EmptyState, FormSection } from "../components/ui";
import { TypedConfirmation, useFeatureResource } from "../components/feature-state";
import { useUnsavedDraft } from "../routing/unsaved-draft";

const blank = (): AuthRecordInput => ({ email: "", password: "", active: true, verified: false, emailVisibility: false, fields: {} });
function fromRecord(record: AuthRecord): AuthRecordInput { return { email: record.email ?? "", active: record.active, verified: record.verified, emailVisibility: record.emailVisibility, fields: record.fields ?? {} }; }
function RecordEditor({ collection, id, port, onDone }: { collection: AuthCollection; id: string | null; port: AuthRecordsPort; onDone(): void }) {
  const read = useCallback(async (signal: AbortSignal) => id === null ? null : port.get(collection.id, id, signal), [collection.id, id, port]);
  const resource = useFeatureResource(read);
  const [input, setInput] = useState<AuthRecordInput>(blank);
  const [json, setJSON] = useState("{}");
  const [saved, setSaved] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [message, setMessage] = useState("");
  const original = useRef("");
  useEffect(() => {
    if (id !== null && !resource.data) return;
    const next = resource.data ? fromRecord(resource.data) : { ...blank(), fields: Object.fromEntries((collection.fields ?? []).filter(field => !field.system && field.required && field.type === "boolean").map(field => [field.name, false])) };
    const text = stringifyJSON(next.fields, 2); setInput(next); setJSON(text);
    const signature = stringifyJSON({ ...next, fields: text }); original.current = signature; setSaved(signature);
  }, [resource.data, id]);
  const dirty = saved !== "" && stringifyJSON({ ...input, fields: json }) !== saved;
  const release = useUnsavedDraft(`auth-record:${collection.id}:${id ?? "new"}`, dirty);
  const patch = (value: Partial<AuthRecordInput>) => setInput(current => ({ ...current, ...value }));
  async function save() {
    let fields: unknown;
    try { fields = parseJSON(json); if (fields === null || Array.isArray(fields) || typeof fields !== "object") throw new Error(); }
    catch { setMessage("Profile fields must be a JSON object."); return; }
    if (!input.email.trim()) { setMessage("Enter an email address."); return; }
    if (id === null && !input.password) { setMessage("Enter an initial password."); return; }
    const value = { ...input, fields: fields as RecordData };
    const result = await resource.run(signal => id === null ? port.create(collection.id, value, signal) : port.update(collection.id, id, value, signal));
    if (result) { release(); onDone(); }
  }
  async function remove() {
    if (id === null) return;
    const result = await resource.run(signal => port.remove(collection.id, id, signal));
    if (result) { release(); onDone(); }
  }
  function cancel() { if (!dirty || window.confirm("Discard the changes to this user?")) { release(); onDone(); } }
  const disabled = resource.busy || resource.loading;
  let profile: RecordData | null = null;
  try { const parsed = parseJSON(json); if (parsed !== null && !Array.isArray(parsed) && typeof parsed === "object") profile = parsed as RecordData; } catch { /* preserve invalid raw JSON until corrected */ }
  const scalarFields = (collection.fields ?? []).filter(field => !field.system && ["text", "uuid", "timestamptz", "boolean"].includes(field.type));
  function editProfileField(name: string, value: string | boolean) { if (!profile) return; setJSON(stringifyJSON({ ...profile, [name]: value }, 2)); }

  return <FormSection className="auth-record-editor" title={id === null ? `Create user in ${collection.name}` : `Edit user · ${id}`}>
    <Button variant="secondary" disabled={resource.busy} onClick={cancel}>Back to collection users</Button>
    {resource.loading && <p role="status">Loading user…</p>}
    {resource.error && <p role="alert">{resource.error} <Button variant="secondary" disabled={resource.busy || dirty} onClick={resource.reload}>Reload user</Button></p>}
    {message && <p role="alert">{message}</p>}
    {!resource.loading && (id === null || resource.data) && <form onSubmit={e => { e.preventDefault(); void save(); }}>
      <fieldset disabled={disabled} className="plain-fields">
        <div className="settings-field-grid">
          {id === null && collection.recordId?.kind === "text" && <label>User ID<input value={input.id ?? ""} onChange={e => patch({ id: e.target.value })} placeholder={collection.recordId.autogeneratePattern ? "Generated when left empty" : "Required text ID"} /></label>}
          <label>User email<input autoFocus type="email" required autoComplete="off" value={input.email} onChange={e => patch({ email: e.target.value })} /></label>
          <label>{id === null ? "Initial password" : "New password"}<input aria-label={id === null ? "Initial password" : "New password"} type="password" autoComplete="new-password" required={id === null} value={input.password ?? ""} onChange={e => patch({ password: e.target.value })} /><span className="settings-help">{id === null ? "The collection password policy applies." : "Leave empty to keep the current password."}</span></label>
        </div>
        <div className="settings-checks">{([['active','Active'],['verified','Email verified'],['emailVisibility','Expose email to authorized clients']] as const).map(([key,label]) => <label key={key}><input type="checkbox" checked={input[key]} onChange={e => patch({ [key]: e.target.checked })} />{label}</label>)}</div>
        {scalarFields.length > 0 && <FormSection title="Profile fields"><div className="settings-field-grid">{scalarFields.map(field => field.type === "boolean" ? <label key={field.name}><input type="checkbox" disabled={!profile} checked={profile?.[field.name] === true} onChange={e => editProfileField(field.name, e.target.checked)} />{field.name}</label> : <label key={field.name}>{field.name}<input aria-label={field.name} disabled={!profile} required={field.required} value={String(profile?.[field.name] ?? "")} onChange={e => editProfileField(field.name, e.target.value)} /><span className="settings-help">{field.help}</span></label>)}</div></FormSection>}
        <details className="advanced-options" open={scalarFields.length === 0 ? true : undefined}><summary>Advanced profile JSON</summary><label>Profile fields JSON<textarea className="access-json" rows={5} autoCapitalize="none" spellCheck={false} value={json} onChange={e => setJSON(e.target.value)} /></label></details>
        <p className="settings-help">Only collection-defined fields are accepted. Existing values are loaded before editing.</p>
        <div className="page-action-buttons"><Button type="submit">{resource.busy ? "Saving…" : "Save user"}</Button><Button variant="secondary" onClick={cancel}>Cancel editing</Button>{id !== null && <Button variant="danger" onClick={() => setConfirmDelete(true)}>Delete user</Button>}</div>
      </fieldset>
    </form>}
    {confirmDelete && id && <TypedConfirmation subject={resource.data?.email ?? id} action="Delete user permanently" busy={resource.busy} onConfirm={() => void remove()} onCancel={() => setConfirmDelete(false)} />}
  </FormSection>;
}
export function AuthRecordsPanel() {
  const { authCollections, capabilities } = useBackend();
  const read = useCallback((signal: AbortSignal) => capabilities?.authCollections === false || !authCollections.records ? Promise.resolve([]) : authCollections.listAuthCollections(signal), [authCollections, capabilities?.authCollections]) as (signal: AbortSignal) => Promise<AuthCollection[]>;
  const realms = useFeatureResource(read);
  const [selected, setSelected] = useState("");
  const [editor, setEditor] = useState<string | null | undefined>();
  const port = authCollections.records;
  if (!port || capabilities?.authCollections === false) return null;
  return <section className="auth-records-panel" aria-labelledby="auth-records-title">
    <div className="access-list-heading"><h3 id="auth-records-title">Collection users</h3><p>Create and edit users within an authentication collection.</p></div>
    {realms.error && <p role="alert">{realms.error} <Button variant="secondary" onClick={realms.reload}>Retry collections</Button></p>}
    <label>Authentication collection<select disabled={realms.loading || editor !== undefined} value={selected} onChange={e => setSelected(e.target.value)}><option value="">Choose a collection…</option>{realms.data?.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
    {realms.loading && <p role="status">Loading authentication collections…</p>}
    {realms.data?.find(item => item.id === selected) && <AuthRecordList key={selected} collection={realms.data.find(item => item.id === selected)!} port={port} editor={editor} setEditor={setEditor} />}
  </section>;
}
function AuthRecordList({ collection, port, editor, setEditor }: { collection: AuthCollection; port: AuthRecordsPort; editor: string | null | undefined; setEditor(value: string | null | undefined): void }) {
  const [limit, setLimit] = useState(50);
  const [query, setQuery] = useState("");
  const [exactID, setExactID] = useState("");
  const read = useCallback((signal: AbortSignal) => port.list(collection.id, limit, signal), [collection.id, limit, port]);
  const resource = useFeatureResource(read);
  if (editor !== undefined) return <RecordEditor key={editor ?? "new"} collection={collection} id={editor} port={port} onDone={() => { setEditor(undefined); resource.reload(); }} />;
  const visible = resource.data?.filter(item => (item.email ?? item.id).toLowerCase().includes(query.toLowerCase()));
  return <div>
    <div className="record-actions"><Button onClick={() => setEditor(null)}>Create user</Button><Button variant="secondary" disabled={resource.loading} onClick={resource.reload}>Refresh users</Button><label>Show users<select value={limit} onChange={e => setLimit(Number(e.target.value))}><option value={50}>Up to 50</option><option value={100}>Up to 100</option></select></label></div>
    <label>Find loaded users<input type="search" value={query} onChange={e => setQuery(e.target.value)} placeholder="Email or ID" /></label>
    {resource.loading && <p role="status">Loading users…</p>}{resource.error && <p role="alert">{resource.error}</p>}
    {!resource.loading && visible?.length === 0 && <EmptyState title="No users found." description="Create a user or change the search." />}
    {!!visible?.length && <div className="table-scroll"><table><thead><tr><th>Email / ID</th><th>Status</th><th>Verification</th><th>Actions</th></tr></thead><tbody>{visible.map(item => <tr key={item.id}><td>{item.email || item.id}<small>{item.email && item.id}</small></td><td>{item.active ? "Active" : "Inactive"}</td><td>{item.verified ? "Verified" : "Unverified"}</td><td><Button variant="secondary" onClick={() => setEditor(item.id)}>Edit user</Button></td></tr>)}</tbody></table></div>}
    {resource.data?.length === limit && <p className="settings-help">The server returns at most {limit} users. To open a user outside this list, enter their exact ID below.</p>}
    <form className="record-actions" onSubmit={e => { e.preventDefault(); if (exactID.trim()) setEditor(exactID.trim()); }}><label>User ID to open<input value={exactID} onChange={e => setExactID(e.target.value)} autoComplete="off" /></label><Button variant="secondary" type="submit" disabled={!exactID.trim()}>Open exact user ID</Button></form>
  </div>;
}
