import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { useBlocker } from "react-router";
import { ApiError } from "../api/http";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
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

/** One route-level guard per tab: in-app navigation asks first, a reload or tab close warns. */
export function UnsavedGuard({ dirty, release }: { dirty: boolean; release?: RefObject<boolean> }) {
  const t = useT();
  const blocker = useBlocker(({ currentLocation, nextLocation }) => dirty && !release?.current && currentLocation.pathname + currentLocation.search !== nextLocation.pathname + nextLocation.search);
  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);
  return (
    <ConfirmDialog
      open={blocker.state === "blocked"}
      title={t("records.discardTitle")}
      message={t("records.discardText")}
      confirmLabel={t("records.discard")}
      danger
      onConfirm={() => blocker.proceed?.()}
      onCancel={() => blocker.reset?.()}
    />
  );
}

/** Save / Discard with a state dot; the form's submit button lives here. */
export function SaveBar({ form, dirty, busy, onDiscard, label }: { form: string; dirty: boolean; busy: boolean; onDiscard: () => void; label?: string }) {
  const t = useT();
  return (
    <div className="auth-savebar">
      <span className="auth-state" data-dirty={dirty ? "" : undefined}>{dirty ? t("auth.unsaved") : t("auth.saved")}</span>
      <Button variant="secondary" size="sm" disabled={!dirty || busy} onClick={onDiscard}>{t("auth.discard")}</Button>
      <Button type="submit" size="sm" form={form} loading={busy} disabled={!dirty}>{label ?? t("auth.save")}</Button>
    </div>
  );
}

/** Message for a client-side problem code. */
export const problemText = (t: Translate, problems: Problems, key: string) => (problems[key] ? t(`authv.${problems[key]}`) : undefined);

/** Server rejections carry no field details; say so once, next to the form. */
export function reportFailure(t: Translate, error: unknown) {
  toast(error instanceof ApiError && error.code === "invalid_request" ? t("auth.rejected") : errorText(t, error), "danger");
}
