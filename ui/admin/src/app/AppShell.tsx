import { Outlet } from "react-router";
import { useT } from "../i18n";
import { AppNav } from "./AppNav";
import { setDrawer, useDrawer, useRail } from "./shell";

export function AppShell() {
  const t = useT();
  const rail = useRail();
  const drawer = useDrawer();
  return (
    <div className="app" data-rail={rail ? "" : undefined} data-drawer={drawer ? "" : undefined}>
      <AppNav />
      <button type="button" className="nav-scrim" aria-label={t("common.close")} onClick={() => setDrawer(false)} />
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
