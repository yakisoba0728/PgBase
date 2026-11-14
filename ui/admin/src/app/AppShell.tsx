import { lazy, Suspense, useState, useEffect, useRef } from "react";
import { Outlet, useLocation, useMatches } from "react-router";
import { PageSkeleton } from "../components/PageSkeleton";
import { useT } from "../i18n";
import { AppNav } from "./AppNav";
import { drawerOpener, setDrawer, useDrawer, useHelp, usePalette, useRail, useSmallScreen } from "./shell";
import { useShellShortcuts } from "./shortcuts";

// Loaded on first use, so the shell chunk carries only the shortcut registry.
const Overlays = lazy(() => import("./CommandPalette").then((module) => ({ default: module.ShellOverlays })));

export function AppShell() {
  const t = useT();
  const location = useLocation();
  const rail = useRail();
  const drawer = useDrawer();
  const palette = usePalette();
  const help = useHelp();
  const small = useSmallScreen();
  const root = useRef<HTMLDivElement>(null);
  // Any navigation (link, palette, shortcut, account menu) closes the drawer.
  useEffect(() => setDrawer(false), [location.pathname]);
  // One Suspense per screen: tab and splat changes inside a screen keep its state; another screen or collection remounts.
  const match = useMatches().at(-1);
  const { tab: _tab, "*": _rest, ...params } = match?.params ?? {};
  const screenKey = `${match?.id}:${JSON.stringify(params)}`;
  useEffect(() => {
    if (!drawer || !small) return;
    // The opener can be the loading skeleton's menu button, replaced once the screen mounts; then the live one gets focus back.
    const previous = drawerOpener();
    const nav = root.current?.querySelector<HTMLElement>(".nav");
    const focus = () => {
      if (!nav?.contains(document.activeElement)) nav?.querySelector<HTMLElement>("a[href], button:not(:disabled)")?.focus();
    };
    // Opening visibility can still be transitioning when the effect runs.
    const frame = requestAnimationFrame(focus);
    nav?.addEventListener("transitionend", focus);
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") setDrawer(false); };
    const trap = (event: KeyboardEvent) => {
      if (event.key !== "Tab") return;
      const nodes = [...(root.current?.querySelectorAll<HTMLElement>(".nav a[href], .nav button:not(:disabled)") ?? [])].filter((element) => element.getClientRects().length);
      const first = nodes[0], last = nodes.at(-1);
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    };
    document.addEventListener("keydown", escape);
    document.addEventListener("keydown", trap);
    return () => { cancelAnimationFrame(frame); nav?.removeEventListener("transitionend", focus); document.removeEventListener("keydown", escape); document.removeEventListener("keydown", trap); (previous?.isConnected ? previous : root.current?.querySelector<HTMLElement>(".nav-open"))?.focus(); };
  }, [drawer, small]);
  // Mounted from first use on, so closing goes through dialog.close() and focus returns to the trigger.
  const [used, setUsed] = useState(false);
  if ((palette || help) && !used) setUsed(true);
  useShellShortcuts();
  return (
    <div ref={root} className="app" data-rail={rail ? "" : undefined} data-drawer={drawer ? "" : undefined}>
      <AppNav />
      <button type="button" className="nav-scrim" aria-label={t("common.close")} onClick={() => setDrawer(false)} />
      <main className="main" inert={drawer && small}>
        <Suspense key={screenKey} fallback={<PageSkeleton />}>
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
