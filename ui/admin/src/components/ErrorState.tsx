import { RiErrorWarningLine, RiRefreshLine } from "@remixicon/react";
import { errorText } from "../app/queries";
import { useT } from "../i18n";
import { Button } from "./Button";
import { EmptyState } from "./Feedback";

/** A failed load: what went wrong, and a retry that refetches. */
export function ErrorState({ error, onRetry, busy }: { error: unknown; onRetry: () => void; busy?: boolean }) {
  const t = useT();
  return (
    <EmptyState icon={RiErrorWarningLine} action={<Button size="sm" variant="secondary" icon={RiRefreshLine} loading={busy} onClick={onRetry}>{t("common.retry")}</Button>}>
      {errorText(t, error)}
    </EmptyState>
  );
}
