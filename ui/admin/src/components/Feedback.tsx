import type { ReactNode } from "react";
import type { RemixiconComponentType } from "@remixicon/react";
import { useT } from "../i18n";

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
