import type { ReactNode } from "react";
import { NavLink } from "react-router";
import { RiMenuLine } from "@remixicon/react";
import { setDrawer } from "../app/shell";
import { useT } from "../i18n";
import { IconButton } from "./Button";

interface PageProps {
  title: ReactNode;
  /** Muted context before the title, e.g. the collection type. */
  crumb?: ReactNode;
  actions?: ReactNode;
  /** Sub-views; each is a route, so the active tab lives in the URL. */
  tabs?: { to: string; label: string; end?: boolean }[];
  children: ReactNode;
}

export function Page({ title, crumb, actions, tabs, children }: PageProps) {
  const t = useT();
  return (
    <>
      <header className="page-header">
        <IconButton className="nav-open" label={t("nav.open")} icon={RiMenuLine} onClick={() => setDrawer(true)} />
        <h1 className="page-title">
          {crumb && <span className="crumb">{crumb} /</span>}
          <span>{title}</span>
        </h1>
        {actions && <div className="page-actions">{actions}</div>}
      </header>
      {tabs && (
        <nav className="page-tabs" aria-label={t("nav.views")}>
          {tabs.map((tab) => (
            <NavLink key={tab.to} to={tab.to} end={tab.end} className="page-tab">
              {tab.label}
            </NavLink>
          ))}
        </nav>
      )}
      <div className="page-body">{children}</div>
    </>
  );
}
