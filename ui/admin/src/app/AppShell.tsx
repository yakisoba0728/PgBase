import { lazy, Suspense } from "react";
import { Outlet } from "react-router";
import { Loader } from "../components/Feedback";
import { useT } from "../i18n";
import { AppNav } from "./AppNav";
import { setDrawer, useDrawer, useHelp, usePalette, useRail } from "./shell";
import { useShellShortcuts } from "./shortcuts";

// Loaded on first use, so the shell chunk carries only the shortcut registry.
const Overlays = lazy(() => import("./CommandPalette").then((module) => ({ default: module.ShellOverlays })));

export function AppShell() {
  const t = useT();
  const rail = useRail();
  const drawer = useDrawer();
  const palette = usePalette();
  const help = useHelp();
  useShellShortcuts();
  return (
    <div className="app" data-rail={rail ? "" : undefined} data-drawer={drawer ? "" : undefined}>
      <AppNav />
      <button type="button" className="nav-scrim" aria-label={t("common.close")} onClick={() => setDrawer(false)} />
      <main className="main">
        <Suspense fallback={<Loader />}>
          <Outlet />
        </Suspense>
      </main>
      {(palette || help) && (
        <Suspense>
          <Overlays />
        </Suspense>
      )}
    </div>
  );
}
