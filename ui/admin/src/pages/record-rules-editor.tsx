import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";

const actions = ["list", "view", "create", "update", "delete"] as const;
const presets = ["owner", "deny", "authenticated", "allow"] as const;
type Action = typeof actions[number];
type PreviewAction = Exclude<Action, "create">;

function preset(value: unknown): string | undefined {
  if (typeof value !== "object" || value === null || !("op" in value)) return undefined;
  const operation = (value as { op: unknown }).op;
  return typeof operation === "string" && (presets as readonly string[]).includes(operation) ? operation : undefined;
}

export function RecordRulesEditor({ collection }: { collection: string }) {
  const { rules } = useBackend();
  const [value, setValue] = useState<Record<string, unknown> | null>(null);
  const [draft, setDraft] = useState("{}");
  const [revision, setRevision] = useState(0);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [previewAction, setPreviewAction] = useState<PreviewAction>("view");
  const [previewUserID, setPreviewUserID] = useState("");
  const [previewRecordIDs, setPreviewRecordIDs] = useState("");
  const [previewResult, setPreviewResult] = useState<Array<{ recordId: string; allowed: boolean }> | null>(null);
  const generation = useRef(0);
  const load = useCallback(() => {
    const current = ++generation.current; setPending(true); setError("");
    void rules.getRules(collection).then((result) => {
      if (current !== generation.current) return;
      setValue(result.rules); setDraft(JSON.stringify(result.rules, null, 2)); setRevision(result.revision);
    }).catch(() => { if (current === generation.current) setError("Unable to load record rules."); }).finally(() => { if (current === generation.current) setPending(false); });
  }, [collection, rules]);
  useEffect(() => { load(); return () => { generation.current += 1; }; }, [load]);
  function setAction(action: Action, operation: string) {
    if (!value) return;
    const next = { ...value, [action]: { op: operation } }; setValue(next); setDraft(JSON.stringify(next, null, 2));
  }
  function save() {
    let parsed: Record<string, unknown>;
    try { parsed = JSON.parse(draft) as Record<string, unknown>; } catch { setError("Rules must be valid JSON."); return; }
    const current = ++generation.current; setPending(true); setError("");
    void rules.setRules(collection, parsed, revision).then((result) => {
      if (current === generation.current) { setValue(result.rules); setDraft(JSON.stringify(result.rules, null, 2)); setRevision(result.revision); }
    }).catch(() => { if (current === generation.current) setError("Unable to save record rules."); }).finally(() => { if (current === generation.current) setPending(false); });
  }
  function preview() {
    let rule: Record<string, unknown>;
    try { rule = JSON.parse(draft) as Record<string, unknown>; } catch { setError("Rules must be valid JSON."); return; }
    const recordIds = previewRecordIDs.split(/\s|,/).filter(Boolean);
    if (recordIds.length === 0 || recordIds.length > 50) { setError("Enter one to fifty record IDs to preview."); return; }
    const current = ++generation.current; setPending(true); setError(""); setPreviewResult(null);
    void rules.previewRule(collection, { rule, action: previewAction, ...(previewUserID.trim() === "" ? {} : { userId: previewUserID.trim() }), recordIds }).then((result) => {
      if (current === generation.current) setPreviewResult(result.items);
    }).catch(() => { if (current === generation.current) setError("Unable to preview record rules."); }).finally(() => { if (current === generation.current) setPending(false); });
  }
  return <section className="record-rules"><h3>Record access rules</h3>
    <p>Owner limits public users to their records. Authenticated allows signed-in users. Allow permits everyone when an anonymous record API is connected.</p>
    {error && <p role="alert">{error}</p>}
    {value && actions.map((action) => <label key={action}>{action}<select disabled={pending} value={preset(value[action]) ?? "advanced"} onChange={(event) => event.target.value !== "advanced" && setAction(action, event.target.value)}><option value="advanced">Advanced JSON</option>{presets.map((item) => <option key={item}>{item}</option>)}</select></label>)}
    <label>Advanced rules JSON<textarea disabled={pending} value={draft} onChange={(event) => setDraft(event.target.value)} rows={12} /></label>
    <button type="button" disabled={pending} onClick={load}>Reload</button><button type="button" disabled={pending} onClick={() => value && setDraft(JSON.stringify(value, null, 2))}>Reset</button><button type="button" disabled={pending} onClick={save}>Save rules</button>
    <fieldset disabled={pending}><legend>Preview saved-record access</legend>
      <p>Preview evaluates this unsaved JSON against existing records. Create is not previewable.</p>
      <label>Action<select value={previewAction} onChange={(event) => setPreviewAction(event.target.value as PreviewAction)}>{(["list", "view", "update", "delete"] as const).map((action) => <option key={action}>{action}</option>)}</select></label>
      <label>User ID (optional for anonymous)<input value={previewUserID} onChange={(event) => setPreviewUserID(event.target.value)} /></label>
      <label>Record IDs (comma or space separated)<textarea value={previewRecordIDs} onChange={(event) => setPreviewRecordIDs(event.target.value)} /></label>
      <button type="button" onClick={preview}>Preview access</button>
    </fieldset>
    {previewResult && <ul aria-label="Rule preview results">{previewResult.map((item) => <li key={item.recordId}>{item.recordId}: {item.allowed ? "allowed" : "denied"}</li>)}</ul>}
  </section>;
}
