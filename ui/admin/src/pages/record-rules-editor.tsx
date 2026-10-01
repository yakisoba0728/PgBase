import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import { parseJSON, stringifyJSON } from "../backend/json";

const actions = ["list", "view", "create", "update", "delete"] as const;
const presets = ["owner", "deny", "authenticated", "allow"] as const;
type Action = typeof actions[number];
type PreviewAction = Exclude<Action, "create">;

function preset(value: unknown): string | undefined {
  if (typeof value !== "object" || value === null || !("op" in value)) return undefined;
  const operation = (value as { op: unknown }).op;
  return typeof operation === "string" && (presets as readonly string[]).includes(operation) ? operation : undefined;
}

function ruleDraft(text: string): Record<string, unknown> | null {
  try {
    const rules = parseJSON(text);
    return typeof rules === "object" && rules !== null && !Array.isArray(rules) ? rules as Record<string, unknown> : null;
  } catch { return null; }
}

export function RecordRulesEditor({ collection }: { collection: string }) {
  const { rules } = useBackend();
  const [value, setValue] = useState<Record<string, unknown> | null>(null);
  const [draft, setDraft] = useState("{}");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [previewAction, setPreviewAction] = useState<PreviewAction>("view");
  const [previewUserID, setPreviewUserID] = useState("");
  const [previewRecordIDs, setPreviewRecordIDs] = useState("");
  const [previewResult, setPreviewResult] = useState<Array<{ recordId: string; allowed: boolean }> | null>(null);
  const draftRules = ruleDraft(draft);
  const generation = useRef(0);
  const load = useCallback(() => {
    const current = ++generation.current; setPending(true); setError("");
    void rules.getRules(collection).then((result) => {
      if (current !== generation.current) return;
      setValue(result.rules); setDraft(stringifyJSON(result.rules, 2));
    }).catch(() => { if (current === generation.current) setError("Unable to load record rules."); }).finally(() => { if (current === generation.current) setPending(false); });
  }, [collection, rules]);
  useEffect(() => { load(); return () => { generation.current += 1; }; }, [load]);
  useEffect(() => { setPreviewResult(null); }, [collection, draft, previewAction, previewUserID, previewRecordIDs]);
  function setAction(action: Action, operation: string) {
    if (!draftRules) { setError("Rules must be a JSON object before changing a preset."); return; }
    setDraft(stringifyJSON({ ...draftRules, [action]: { op: operation } }, 2)); setError("");
  }
  function save() {
    if (!draftRules) { setError("Rules must be a JSON object."); return; }
    const current = ++generation.current; setPending(true); setError("");
    void rules.setRules(collection, draftRules).then((result) => {
      if (current === generation.current) { setValue(result.rules); setDraft(stringifyJSON(result.rules, 2)); }
    }).catch(() => { if (current === generation.current) setError("Unable to save record rules."); }).finally(() => { if (current === generation.current) setPending(false); });
  }
  function preview() {
    let rule: Record<string, unknown>;
    try {
      rule = draftRules?.[previewAction] as Record<string, unknown>;
      if (typeof rule !== "object" || rule === null || Array.isArray(rule)) throw new Error("missing rule");
    } catch { setError("Rules must contain a JSON object for the selected action."); return; }
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
    {value && actions.map((action) => <label key={action}>{action}<select disabled={pending} value={preset(draftRules?.[action]) ?? "advanced"} onChange={(event) => event.target.value !== "advanced" && setAction(action, event.target.value)}><option value="advanced">Advanced JSON</option>{presets.map((item) => <option key={item}>{item}</option>)}</select></label>)}
    <label>Advanced rules JSON<textarea disabled={pending} value={draft} onChange={(event) => setDraft(event.target.value)} rows={12} /></label>
    <button type="button" disabled={pending} onClick={load}>Reload</button><button type="button" disabled={pending} onClick={() => value && setDraft(stringifyJSON(value, 2))}>Reset</button><button type="button" disabled={pending} onClick={save}>Save rules</button>
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
