import type { ReactNode } from "react";
import { RiErrorWarningLine, type RemixiconComponentType } from "@remixicon/react";
import { errorText } from "../app/queries";
import { useT } from "../i18n";
import { Button } from "./Button";

export function Loader() {
  const t = useT();
  return (
    <div className="loader-center" role="status">
      <span className="loader" aria-hidden />
      <span className="visually-hidden">{t("common.loading")}</span>
    </div>
  );
}

export function EmptyState({ icon: Icon, children, action }: { icon?: RemixiconComponentType; children: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      {Icon && <Icon size={24} aria-hidden />}
      <p>{children}</p>
      {action}
    </div>
  );
}

/** A failed load, in words, with a retry (distinct from an empty result). */
export function ErrorState({ error, onRetry }: { error: unknown; onRetry: () => unknown }) {
  const t = useT();
  return (
    <EmptyState icon={RiErrorWarningLine} action={<Button size="sm" variant="secondary" onClick={() => void onRetry()}>{t("common.retry")}</Button>}>
      {errorText(t, error)}
    </EmptyState>
  );
}
