import { useRef, useState, type ReactNode } from "react";
import { NavLink, useLocation } from "react-router";
import { RiMenuLine } from "@remixicon/react";
import { setDrawer } from "../app/shell";
import { useT } from "../i18n";
import { IconButton } from "./Button";
import { useTabIndicator } from "./Tabs";

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
  const row = useRef<HTMLElement>(null);
  useTabIndicator(row, Boolean(tabs));
  // A sibling tab (or sub-path) replacing the body cross-fades it: the attribute alternates so the animation restarts.
  const { pathname } = useLocation();
  const [swap, setSwap] = useState({ pathname, parity: 0 });
  if (swap.pathname !== pathname) setSwap({ pathname, parity: swap.parity + 1 });
  return (
    <>
      <header className="page-header">
        <IconButton className="nav-open" label={t("nav.open")} icon={RiMenuLine} onClick={(event) => setDrawer(true, event.currentTarget)} />
        <h1 className="page-title">
          {crumb && <span className="crumb">{crumb} /</span>}
          <span>{title}</span>
        </h1>
        {actions && <div className="actions">{actions}</div>}
      </header>
      {tabs && (
        <nav ref={row} className="page-tabs" aria-label={t("nav.views")}>
          {tabs.map((tab) => (
            <NavLink key={tab.to} to={tab.to} end={tab.end} className="page-tab">
              {tab.label}
            </NavLink>
          ))}
        </nav>
      )}
      <div className="page-body" data-swap={swap.parity ? (swap.parity % 2 ? "a" : "b") : undefined}>{children}</div>
    </>
  );
}
