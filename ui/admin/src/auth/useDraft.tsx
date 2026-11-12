import { useCallback, useRef, useState } from "react";
import { ApiError } from "../api/http";
import { Notice } from "../components/Feedback";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import type { Problems } from "./forms";

/** A form draft compared against its loaded value. Callers key the form by the loaded data,
 * so a save or reload remounts it with a fresh base. */
export function useDraft<T extends object>(initial: T) {
  const [draft, setDraft] = useState(initial);
  const [problems, setProblems] = useState<Problems>({});
  // Set right before a save or delete navigates away, so the guard lets that one through.
  const release = useRef(false);
  const dirty = JSON.stringify(draft) !== JSON.stringify(initial);
  const patch = useCallback((changes: Partial<T>) => setDraft((previous) => ({ ...previous, ...changes })), []);
  const reset = useCallback(() => {
    setDraft(initial);
    setProblems({});
  }, [initial]);
  return { draft, patch, dirty, reset, problems, setProblems, release };
}

/** Message for a client-side problem code. */
export const problemText = (t: Translate, problems: Problems, key: string) => (problems[key] ? t(`authv.${problems[key]}`) : undefined);

/** Thrown before a save when the stored value moved since the form loaded it; nothing was written. */
export class StaleError extends Error {}

/**
 * Policy, providers, templates and SMTP have no revision, so a save re-reads the stored value
 * first and refuses (unless forced) when another administrator changed it in the meantime.
 */
export async function assertUnchanged<T>(force: boolean, read: () => Promise<T>, base: T): Promise<void> {
  if (!force && JSON.stringify(await read()) !== JSON.stringify(base ?? null)) throw new StaleError();
}

/** Explains a refused stale save: load the stored value (dropping the draft) or overwrite it. */
export function StaleNotice({ busy, onReload, onOverwrite }: { busy?: boolean; onReload: () => void; onOverwrite: () => void }) {
  const t = useT();
  return (
    <Notice tone="danger" actions={<>
      <Button size="sm" variant="secondary" onClick={onReload}>{t("auth.staleReload")}</Button>
      <Button size="sm" variant="danger" loading={busy} onClick={onOverwrite}>{t("auth.staleOverwrite")}</Button>
    </>}>{t("auth.stale")}</Notice>
  );
}

/** Server rejections carry no field details; say so once, next to the form. */
export function reportFailure(t: Translate, error: unknown) {
  if (error instanceof StaleError) return;
  toast(error instanceof ApiError && error.code === "invalid_request" ? t("auth.rejected") : errorText(t, error), "danger");
}
