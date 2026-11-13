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

/**
 * Policy, providers, templates and SMTP saves send the loaded `etag` as If-Match; the server refuses
 * the write (412) when another administrator changed the value in the meantime, and nothing is written.
 */
export const isStale = (error: unknown) => error instanceof ApiError && error.status === 412;

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

/** The fields a server rejection names (error.details), or none. */
export function rejectedFields(error: unknown): string[] {
  return error instanceof ApiError && error.details && typeof error.details === "object" ? Object.keys(error.details) : [];
}

/** A stale save shows its notice instead; a rejection names the fields the server reported. */
export function reportFailure(t: Translate, error: unknown) {
  if (isStale(error)) return;
  const fields = rejectedFields(error);
  if (fields.length) toast(t("auth.rejectedFields", { fields: fields.join(", ") }), "danger");
  else toast(error instanceof ApiError && error.code === "invalid_request" ? t("auth.rejected") : errorText(t, error), "danger");
}
