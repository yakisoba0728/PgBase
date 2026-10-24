import { useHasUnsavedDrafts, useUnsavedDraft } from "../routing/unsaved-draft";
import { AuthRecordsPanel } from "./auth-records-panel";
import { AuthCollectionSchema } from "../components/auth-collection-schema";
import { AuthTokenPolicies } from "../components/auth-token-policies";
import { RecordIDPolicyEditor } from "./record-id-policy-editor";
import type { RecordIDPolicy } from "../backend/ports";
import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { AuthCollection } from "../backend/ports";
import { AuthAdministrationPanel } from "../components/auth-administration-panel";
import { Button, FormSection, PageHeading } from "../components/ui";

function profileSchemaText(schema: Record<string, string>): string { return JSON.stringify(schema, null, 2); }

function parseProfileSchema(value: string): Record<string, string> | null {
  try {
    const parsed: unknown = JSON.parse(value);
    if (parsed === null || Array.isArray(parsed) || typeof parsed !== "object") return null;
    if (Object.values(parsed).some((field) => typeof field !== "string")) return null;
    return parsed as Record<string, string>;
  } catch { return null; }
}

/** Manage separately registered public-user realms and their scalar profiles. */
export function AuthCollectionsPage() {
  const { authCollections, capabilities } = useBackend();
  const hasUnsavedDrafts = useHasUnsavedDrafts();
  const [items, setItems] = useState<AuthCollection[]>([]);
  const [recordId, setRecordID] = useState<RecordIDPolicy | undefined>();
  const [name, setName] = useState("");
  const [newProfileSchema, setNewProfileSchema] = useState("{}");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [structuredDirty, setStructuredDirty] = useState<Set<string>>(() => new Set());
  const onStructuredDirty = useCallback((id: string, dirty: boolean) => setStructuredDirty(current => { if (current.has(id) === dirty) return current; const next = new Set(current); if (dirty) next.add(id); else next.delete(id); return next; }), []);
  const basicDirty = name !== "" || newProfileSchema !== "{}" || recordId !== undefined || items.some(item => drafts[item.id] !== undefined && drafts[item.id] !== profileSchemaText(item.profileSchema));
  useUnsavedDraft("auth-collections-basic", basicDirty);
  const generation = useRef(0);
  const mounted = useRef(true);

  const load = useCallback(() => {
    const issued = ++generation.current;
    const controller = new AbortController();
    setLoading(true); setError(null);
    void authCollections.listAuthCollections(controller.signal).then((received) => {
      if (!mounted.current || generation.current !== issued) return;
      setItems(received);
      setDrafts(Object.fromEntries(received.map((item) => [item.id, profileSchemaText(item.profileSchema)])));
    }).catch(() => {
      if (mounted.current && generation.current === issued && !controller.signal.aborted) setError("Unable to load auth collections. Try again.");
    }).finally(() => {
      if (mounted.current && generation.current === issued) setLoading(false);
    });
    return () => controller.abort();
  }, [authCollections]);

  useEffect(() => {
    mounted.current = true;
    const cancel = load();
    return () => { mounted.current = false; cancel(); generation.current += 1; };
  }, [load]);

  function withMutation(action: (signal: AbortSignal) => Promise<void>): void {
    if (pending) return;
    const issued = ++generation.current;
    const controller = new AbortController();
    setPending(true); setError(null);
    void action(controller.signal).catch(() => {
      if (mounted.current && generation.current === issued && !controller.signal.aborted) setError("Unable to save auth collection. Try again.");
    }).finally(() => {
      if (mounted.current && generation.current === issued) setPending(false);
    });
  }

  function create(): void {
    const profileSchema = parseProfileSchema(newProfileSchema);
    if (name.trim() === "") { setError("Enter an auth collection name."); return; }
    if (profileSchema === null) { setError("Profile schema must be a JSON object whose field types are strings."); return; }
    withMutation(async (signal) => {
      const created = await authCollections.createAuthCollection({ id: crypto.randomUUID(), name: name.trim(), registrationEnabled: false, profileSchema, ...(recordId ? { recordId } : {}) }, signal);
      if (!mounted.current) return;
      setItems((current) => [...current, created]);
      setDrafts((current) => ({ ...current, [created.id]: profileSchemaText(created.profileSchema) }));
      setName(""); setNewProfileSchema("{}"); setRecordID(undefined); setNotice("Auth collection created.");
    });
  }

  function save(item: AuthCollection, registrationEnabled = item.registrationEnabled): void {
    const profileSchema = parseProfileSchema(drafts[item.id] ?? profileSchemaText(item.profileSchema));
    if (profileSchema === null) { setError("Profile schema must be a JSON object whose field types are strings."); return; }
    withMutation(async (signal) => {
      const updated = await authCollections.updateAuthCollection(item.id, { registrationEnabled, profileSchema, ...(item.fields ? { fields: item.fields.filter(field => !field.system), rules: item.rules } : {}) }, signal);
      if (!mounted.current) return;
      setItems((current) => current.map((entry) => entry.id === item.id ? updated : entry));
      setDrafts((current) => ({ ...current, [item.id]: profileSchemaText(updated.profileSchema) })); setNotice(`${item.name} settings saved.`);
    });
  }

  function remove(item: AuthCollection): void {
    withMutation(async (signal) => {
      await authCollections.deleteAuthCollection(item.id, signal);
      if (mounted.current) setItems((current) => current.filter((entry) => entry.id !== item.id));
    });
  }

  return <section className="access-page auth-collections-page">
    <PageHeading title="Auth collections" description="Manage separate registration settings and profile fields for each auth collection." actions={<Button disabled={pending || hasUnsavedDrafts || basicDirty || structuredDirty.size > 0} onClick={() => { if (!hasUnsavedDrafts) load(); }} variant="secondary">Reload</Button>} />
    {capabilities?.users === false && <AuthRecordsPanel />}
    {notice && <p role="status">{notice}</p>}
    {error && <p className="access-feedback form-error" role="alert">{error}</p>}
    <FormSection disabled={pending} title="Create auth collection">
      <div className="access-form-grid">
        <label className="access-field">Name <input aria-label="Auth collection name" placeholder="e.g. members" value={name} onChange={(event) => setName(event.target.value)} /><span className="access-help">New collections start with registration disabled.</span></label>
        <label className="access-field">Profile schema JSON <textarea aria-label="New profile schema" autoCapitalize="none" className="access-json" rows={3} spellCheck={false} value={newProfileSchema} onChange={(event) => setNewProfileSchema(event.target.value)} /></label>
      </div>
      <RecordIDPolicyEditor value={recordId} locked={false} disabled={pending} onChange={setRecordID} />
      <div className="access-form-actions"><Button onClick={create}>Create auth collection</Button></div>
    </FormSection>
    <div className="access-list-heading"><h3>Existing collections</h3><p>Update profile fields and registration for each collection.</p></div>
    {loading ? <p role="status">Loading auth collections…</p> : <ul aria-label="Auth collections" className="access-item-list">{items.map((item) => <li key={item.id}>
      <div className="access-item-heading"><h3>{item.name}</h3><label className="access-toggle"><input disabled={pending || structuredDirty.has(item.id)} checked={item.registrationEnabled} onChange={(event) => save(item, event.target.checked)} type="checkbox" />Registration enabled</label></div>
      <details className="advanced-options"><summary>Compatibility profile schema</summary>      <label className="access-field">Profile schema JSON<textarea disabled={structuredDirty.has(item.id)} readOnly={item.fields !== undefined} aria-label={`${item.name} profile schema`} autoCapitalize="none" className="access-json" rows={4} spellCheck={false} value={drafts[item.id] ?? profileSchemaText(item.profileSchema)} onChange={(event) => setDrafts((current) => ({ ...current, [item.id]: event.target.value }))} /></label></details>
      {item.fields !== undefined && <p className="settings-help">Use the structured fields editor below to preserve validation and access settings.</p>}
      <div className="access-item-actions">{item.fields === undefined && <Button disabled={pending || structuredDirty.has(item.id)} onClick={() => save(item)}>Save</Button>}<Button disabled={pending || structuredDirty.has(item.id) || item.name === "users"} onClick={() => remove(item)} variant="danger">Delete</Button></div>
      {item.name === "users" && <p className="access-help">The default users realm cannot be deleted.</p>}
      <AuthCollectionSchema authCollections={items} disabled={drafts[item.id] !== undefined && drafts[item.id] !== profileSchemaText(item.profileSchema)} onDirty={onStructuredDirty} collection={item} onSaved={saved => { setNotice(`${saved.name} fields and rules saved.`); setItems(current => current.map(v => v.id === saved.id ? saved : v)); setDrafts(current => ({ ...current, [saved.id]: current[saved.id] === profileSchemaText(item.profileSchema) ? profileSchemaText(saved.profileSchema) : current[saved.id] })); }} />
      <AuthTokenPolicies collectionId={item.id} />
      {capabilities?.authAdministration !== false && <AuthAdministrationPanel collectionId={item.id} />}
    </li>)}</ul>}
    {!loading && !error && items.length === 0 && <p className="access-empty">No auth collections yet. Create one above to get started.</p>}
  </section>;
}
