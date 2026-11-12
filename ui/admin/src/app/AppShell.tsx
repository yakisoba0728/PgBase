import { lazy, Suspense, useState } from "react";
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
  // Mounted from first use on, so closing goes through dialog.close() and focus returns to the trigger.
  const [used, setUsed] = useState(false);
  if ((palette || help) && !used) setUsed(true);
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
      {used && (
        <Suspense>
          <Overlays />
        </Suspense>
      )}
    </div>
  );
}
