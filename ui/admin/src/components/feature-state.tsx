import { useEffect, useRef, useState } from "react";
import { Button } from "./ui";

/** Abort old reads and ignore completions after leaving the resource. Mutations
 * are never auto-retried: a lost response can still have committed on the server. */
export function useFeatureResource<T>(loader: (signal: AbortSignal) => Promise<T>) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);
  const controller = useRef<AbortController | null>(null);
  const alive = useRef(false);
  const pending = useRef(false);
  useEffect(() => { alive.current = true; return () => { alive.current = false; controller.current?.abort(); }; }, []);
  useEffect(() => {
    const c = new AbortController(); controller.current = c; setLoading(true); setError("");
    void loader(c.signal).then(value => { if (!c.signal.aborted) setData(value); }).catch(() => { if (!c.signal.aborted) setError("Unable to load this data. Try again."); }).finally(() => { if (!c.signal.aborted) setLoading(false); });
    return () => c.abort();
  }, [loader, revision]);
  async function run<R>(action: (signal: AbortSignal) => Promise<R>): Promise<{ value: R } | undefined> {
    if (pending.current) return;
    pending.current = true; setBusy(true); setError("");
    controller.current?.abort(); const c = new AbortController(); controller.current = c;
    try { const value = await action(c.signal); if (alive.current && !c.signal.aborted) return { value }; }
    catch { if (alive.current && !c.signal.aborted) setError("The server could not confirm the change. Reload the data before retrying."); }
    finally { pending.current = false; if (alive.current) { setBusy(false); setLoading(false); } }
  }
  return { data, setData, error, setError, loading, busy, run, reload: () => { if (!pending.current) setRevision(v => v + 1); } };
}
export function TypedConfirmation({ subject, action, busy, onConfirm, onCancel }: { subject: string; action: string; busy: boolean; onConfirm(): void; onCancel(): void }) {
  const [typed, setTyped] = useState("");
  return <div className="inline-confirmation" role="group" aria-label={`Confirm ${action}`}>
    <label>Type {subject} to confirm {action.toLowerCase()}<input autoFocus autoComplete="off" value={typed} disabled={busy} onChange={e => setTyped(e.target.value)} /></label>
    <div className="page-action-buttons"><Button variant="danger" disabled={busy || typed !== subject} onClick={onConfirm}>{busy ? "Working…" : action}</Button><Button variant="secondary" disabled={busy} onClick={onCancel}>Cancel</Button></div>
  </div>;
}

/** One owner per mounted form prevents an old completion releasing a newer
 * form's draft after navigating away and back to the same resource. */
export function useMutationScope() {
  const mounted = useRef(false);
  const active = useRef<AbortController | null>(null);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; active.current?.abort(); }; }, []);
  return {
    begin: () => { if (active.current) return null; active.current = new AbortController(); return active.current.signal; },
    current: (signal: AbortSignal) => mounted.current && active.current?.signal === signal && !signal.aborted,
    end: (signal: AbortSignal) => { if (active.current?.signal === signal) active.current = null; },
  };
}
