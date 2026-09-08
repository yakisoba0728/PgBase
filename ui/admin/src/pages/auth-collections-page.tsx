import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { AuthCollection } from "../backend/ports";

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
  const { authCollections } = useBackend();
  const [items, setItems] = useState<AuthCollection[]>([]);
  const [name, setName] = useState("");
  const [newProfileSchema, setNewProfileSchema] = useState("{}");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [pending, setPending] = useState(false);
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
      const created = await authCollections.createAuthCollection({ id: crypto.randomUUID(), name: name.trim(), registrationEnabled: false, profileSchema }, signal);
      if (!mounted.current) return;
      setItems((current) => [...current, created]);
      setDrafts((current) => ({ ...current, [created.id]: profileSchemaText(created.profileSchema) }));
      setName(""); setNewProfileSchema("{}");
    });
  }

  function save(item: AuthCollection, registrationEnabled = item.registrationEnabled): void {
    const profileSchema = parseProfileSchema(drafts[item.id] ?? profileSchemaText(item.profileSchema));
    if (profileSchema === null) { setError("Profile schema must be a JSON object whose field types are strings."); return; }
    withMutation(async (signal) => {
      const updated = await authCollections.updateAuthCollection(item.id, { registrationEnabled, profileSchema }, signal);
      if (!mounted.current) return;
      setItems((current) => current.map((entry) => entry.id === item.id ? updated : entry));
      setDrafts((current) => ({ ...current, [item.id]: profileSchemaText(updated.profileSchema) }));
    });
  }

  function remove(item: AuthCollection): void {
    withMutation(async (signal) => {
      await authCollections.deleteAuthCollection(item.id, signal);
      if (mounted.current) setItems((current) => current.filter((entry) => entry.id !== item.id));
    });
  }

  return <section className="auth-collections-page">
    <h2>Auth collections</h2><p>Each realm has separate registration and scalar profile fields.</p>
    {error && <p className="form-error" role="alert">{error}</p>}
    <fieldset disabled={pending}><legend>Create auth collection</legend>
      <label>Name <input aria-label="Auth collection name" value={name} onChange={(event) => setName(event.target.value)} /></label>
      <label>Profile schema JSON <textarea aria-label="New profile schema" value={newProfileSchema} onChange={(event) => setNewProfileSchema(event.target.value)} /></label>
      <button type="button" onClick={create}>Create auth collection</button><button type="button" onClick={load}>Reload</button>
    </fieldset>
    {loading ? <p>Loading auth collections…</p> : <ul aria-label="Auth collections">{items.map((item) => <li key={item.id}>
      <h3>{item.name}</h3><label><input disabled={pending} checked={item.registrationEnabled} onChange={(event) => save(item, event.target.checked)} type="checkbox" />Registration enabled</label>
      <label>Profile schema JSON<textarea aria-label={`${item.name} profile schema`} value={drafts[item.id] ?? profileSchemaText(item.profileSchema)} onChange={(event) => setDrafts((current) => ({ ...current, [item.id]: event.target.value }))} /></label>
      <button disabled={pending} type="button" onClick={() => save(item)}>Save</button><button disabled={pending || item.name === "users"} type="button" onClick={() => remove(item)}>Delete</button>
      {item.name === "users" && <p>The default users realm cannot be deleted.</p>}
    </li>)}</ul>}
  </section>;
}
