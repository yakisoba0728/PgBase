import { useRef, type KeyboardEvent, type MouseEvent } from "react";
import { Link } from "react-router";
import { RiPuzzle2Line, RiSearchLine, RiSidebarFoldLine, RiSidebarUnfoldLine } from "@remixicon/react";
import { keyLabel } from "./keys";
import { CollectionNav } from "../collections/CollectionNav";
import { IconButton } from "../components/Button";
import { NavGroup, NavItem } from "../components/Nav";
import { useTabIndicator } from "../components/Tabs";
import { useT } from "../i18n";
import { useExtensionScreens } from "./extensions";
import { AccountMenu, LanguageMenu, ThemeMenu } from "./menus";
import { navGroups } from "./navigation";
import { RunningFeed } from "./RunningFeed";
import { setDrawer, setPalette, toggleRail, useRail } from "./shell";

/** The single navigation column: wordmark, Data / Auth / Operations / System, then account controls. */
export function AppNav() {
  const t = useT();
  const rail = useRail();
  const screens = useExtensionScreens();
  // In the drawer, following a link closes it.
  const onClick = (event: MouseEvent) => {
    if ((event.target as HTMLElement).closest("a")) setDrawer(false);
  };
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "Escape") setDrawer(false);
  };
  // The current item's highlight glides between items (layout.css).
  const scroll = useRef<HTMLDivElement>(null);
  useTabIndicator(scroll, true, "y");
  return (
    <aside className="nav" aria-label={t("nav.main")} onClick={onClick} onKeyDown={onKeyDown}>
      <div className="nav-top">
        <Link to="/" className="wordmark">
          <b>Pg</b>Base
        </Link>
        <IconButton className="nav-search" label={`${t("palette.open")} (${keyLabel("mod+k").join("")})`} icon={RiSearchLine} onClick={() => setPalette(true)} />
        <IconButton className="nav-collapse" label={rail ? t("nav.expand") : t("nav.collapse")} icon={rail ? RiSidebarUnfoldLine : RiSidebarFoldLine} onClick={toggleRail} />
      </div>
      <div ref={scroll} className="nav-scroll">
        <CollectionNav />
        {navGroups.map((group) => (
          <NavGroup key={group.label} title={t(group.label)}>
            {group.items.map((item) => (
              <NavItem key={item.path} to={`/${item.path}`} icon={item.icon}>{t(item.label)}</NavItem>
            ))}
          </NavGroup>
        ))}
        {screens.length > 0 && (
          <NavGroup title={t("nav.extensions")}>
            {screens.map((screen) => (
              <NavItem key={screen.id} to={`/ext/${screen.id}`} icon={RiPuzzle2Line}>{screen.label}</NavItem>
            ))}
          </NavGroup>
        )}
      </div>
      <RunningFeed />
      <div className="nav-bottom">
        <AccountMenu />
        <LanguageMenu />
        <ThemeMenu />
      </div>
    </aside>
  );
}
