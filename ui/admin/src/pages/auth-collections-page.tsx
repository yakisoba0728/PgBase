import { t, message as bilingual, type Message, useLocale } from "../i18n";
import { useHasUnsavedDrafts, useUnsavedDraft } from "../routing/unsaved-draft";
import { AuthRecordsPanel } from "./auth-records-panel";
import { AuthCollectionSchema } from "../components/auth-collection-schema";
import { AuthTokenPolicies } from "../components/auth-token-policies";
import { TypedConfirmation } from "../components/feature-state";
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
  useLocale();
  const { authCollections, capabilities } = useBackend();
  const hasUnsavedDrafts = useHasUnsavedDrafts();
  const [items, setItems] = useState<AuthCollection[]>([]);
  const [recordId, setRecordID] = useState<RecordIDPolicy | undefined>();
  const [name, setName] = useState("");
  const [query, setQuery] = useState("");
  const [deleting, setDeleting] = useState<string | null>(null);
  const [newProfileSchema, setNewProfileSchema] = useState("{}");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<Message>("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
  const [structuredDirty, setStructuredDirty] = useState<Set<string>>(() => new Set());
  const onStructuredDirty = useCallback((id: string, dirty: boolean) => setStructuredDirty(current => { if (current.has(id) === dirty) return current; const next = new Set(current); if (dirty) next.add(id); else next.delete(id); return next; }), []);
  const basicDirty = name !== "" || newProfileSchema !== "{}" || recordId !== undefined || items.some(item => drafts[item.id] !== undefined && drafts[item.id] !== profileSchemaText(item.profileSchema));
  useUnsavedDraft("auth-collections-basic", basicDirty);
  const generation = useRef(0);
  const mounted = useRef(true);
  const mutationPending = useRef(false);

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
    if (mutationPending.current) return;
    mutationPending.current = true;
    const issued = ++generation.current;
    const controller = new AbortController();
    setPending(true); setError(null);
    void action(controller.signal).catch(() => {
      if (mounted.current && generation.current === issued && !controller.signal.aborted) setError("Unable to save auth collection. Try again.");
    }).finally(() => {
      mutationPending.current = false;
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
      setDrafts((current) => ({ ...current, [item.id]: profileSchemaText(updated.profileSchema) })); setNotice(bilingual(`${item.name} settings saved.`, `${item.name} 설정을 저장했습니다.`));
    });
  }

  function remove(item: AuthCollection): void {
    if (hasUnsavedDrafts) { setError("Save or discard unsaved changes before deleting an auth collection."); return; }
    withMutation(async (signal) => {
      await authCollections.deleteAuthCollection(item.id, signal);
      if (mounted.current) { setItems((current) => current.filter((entry) => entry.id !== item.id)); setDeleting(null); setNotice(bilingual(`${item.name} deleted.`, `${item.name} 컬렉션을 삭제했습니다.`)); }
    });
  }

  return <section className="access-page auth-collections-page">
    <PageHeading title={t("Auth collections")} description={t("Manage separate registration settings and profile fields for each auth collection.")} actions={<Button disabled={pending || hasUnsavedDrafts || basicDirty || structuredDirty.size > 0} onClick={() => { if (!hasUnsavedDrafts) load(); }} variant="secondary">{t("Reload")}</Button>} />
    {capabilities?.users === false && <AuthRecordsPanel />}
    {notice && <p role="status">{t(notice)}</p>}
    {error && <p className="access-feedback form-error" role="alert">{t(error)}</p>}
    <FormSection disabled={pending} title={t("Create auth collection")}>
      <div className="access-form-grid">
        <label className="access-field">{t("Name")}<input aria-label={t("Auth collection name")} placeholder={t("e.g. members")} value={name} onChange={(event) => setName(event.target.value)} /><span className="access-help">{t("New collections start with registration disabled.")}</span></label>
        <label className="access-field">{t("Profile schema JSON")}<textarea aria-label={t("New profile schema")} autoCapitalize="none" className="access-json" rows={3} spellCheck={false} value={newProfileSchema} onChange={(event) => setNewProfileSchema(event.target.value)} /></label>
      </div>
      <RecordIDPolicyEditor value={recordId} locked={false} disabled={pending} onChange={setRecordID} />
      <div className="access-form-actions"><Button onClick={create}>{t("Create auth collection")}</Button></div>
    </FormSection>
    <div className="access-list-heading"><h3>{t("Existing collections")}</h3><p>{t("Update profile fields and registration for each collection.")}</p></div>
    <div className="access-search-toolbar"><label>{t("Search auth collections")}<input type="search" placeholder={t("Collection name…")} value={query} onChange={event => setQuery(event.target.value)} /></label><span className="settings-help">{t(bilingual(`${items.length} collections`, `${items.length}개 컬렉션`))}</span></div>
    {loading ? <p role="status">{t("Loading auth collections…")}</p> : <ul aria-label={t("Auth collections")} className="access-item-list">{items.map((item) => <li key={item.id} className="auth-collection-card" hidden={!item.name.toLowerCase().includes(query.trim().toLowerCase())}>
      <div className="access-item-heading"><h3>{item.name}</h3><label className="access-toggle"><input disabled={pending || structuredDirty.has(item.id)} checked={item.registrationEnabled} onChange={(event) => save(item, event.target.checked)} type="checkbox" />{t("Registration enabled")}</label></div>
      <details className="advanced-options"><summary>{t("Compatibility profile schema")}</summary>      <label className="access-field">{t("Profile schema JSON")}<textarea disabled={structuredDirty.has(item.id)} readOnly={item.fields !== undefined} aria-label={t(bilingual(`${item.name} profile schema`, `${item.name} 프로필 스키마`))} autoCapitalize="none" className="access-json" rows={4} spellCheck={false} value={drafts[item.id] ?? profileSchemaText(item.profileSchema)} onChange={(event) => setDrafts((current) => ({ ...current, [item.id]: event.target.value }))} /></label></details>
      {item.fields !== undefined && <p className="settings-help">{t("Use the structured fields editor below to preserve validation and access settings.")}</p>}
      <div className="access-item-actions">{item.fields === undefined && <Button disabled={pending || structuredDirty.has(item.id)} onClick={() => save(item)}>{t("Save")}</Button>}<Button disabled={pending || hasUnsavedDrafts || structuredDirty.has(item.id) || item.name === "users"} onClick={() => setDeleting(item.id)} variant="danger">{t("Delete")}</Button></div>
      {deleting === item.id && <TypedConfirmation subject={item.name} action={t("Delete auth collection")} busy={pending} onConfirm={() => remove(item)} onCancel={() => setDeleting(null)} />}
      {item.name === "users" && <p className="access-help">{t("The default users realm cannot be deleted.")}</p>}
      <AuthCollectionSchema authCollections={items} disabled={drafts[item.id] !== undefined && drafts[item.id] !== profileSchemaText(item.profileSchema)} onDirty={onStructuredDirty} collection={item} onSaved={saved => { setNotice(bilingual(`${saved.name} fields and rules saved.`, `${saved.name} 필드와 규칙을 저장했습니다.`)); setItems(current => current.map(v => v.id === saved.id ? saved : v)); setDrafts(current => ({ ...current, [saved.id]: current[saved.id] === profileSchemaText(item.profileSchema) ? profileSchemaText(saved.profileSchema) : current[saved.id] })); }} />
      <AuthTokenPolicies collectionId={item.id} />
      {capabilities?.authAdministration !== false && <AuthAdministrationPanel collectionId={item.id} />}
    </li>)}</ul>}
    {!loading && items.length > 0 && !items.some(item => item.name.toLowerCase().includes(query.trim().toLowerCase())) && <p role="status">{t(bilingual(`No auth collections match “${query}”.`, `“${query}”와 일치하는 인증 컬렉션이 없습니다.`))}</p>}
    {!loading && !error && items.length === 0 && <p className="access-empty">{t("No auth collections yet. Create one above to get started.")}</p>}
  </section>;
}
