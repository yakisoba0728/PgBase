/** Movement is off: view transitions are skipped and JS-driven animations (FLIP, staggers) do not run. */
export const reducedMotion = () => typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;

const canTransition = () => typeof document !== "undefined" && typeof document.startViewTransition === "function" && !document.hidden && !reducedMotion();

/**
 * Names what a view transition snapshots: `data-vt` on <html> (base.css scopes view-transition-name by it) for as
 * long as the transition runs, so other transitions (toasts) never capture the page. Cleared once no transition is
 * active, with a timer as well, since a hidden tab runs no frames.
 */
export function markTransition(kind: string) {
  const root = document.documentElement;
  root.dataset.vt = kind;
  const begun = performance.now();
  let seen = false;
  const clear = () => {
    if (root.dataset.vt === kind) delete root.dataset.vt;
  };
  const watch = () => {
    let active = false;
    try {
      active = root.matches(":active-view-transition");
    } catch {
      return clear();
    }
    seen ||= active;
    if ((seen && !active) || performance.now() - begun > 4000) return clear();
    requestAnimationFrame(watch);
  };
  requestAnimationFrame(watch);
  setTimeout(clear, 5000);
}

/** Runs a synchronous DOM update inside a view transition of `kind` where one can run (theme switch); otherwise at once. */
export function withTransition(kind: string, update: () => void) {
  if (!canTransition()) return update();
  markTransition(kind);
  const transition = document.startViewTransition(update);
  transition.ready.catch(() => {});
  transition.finished.catch(() => {});
}

const guestPaths = ["/login", "/setup", "/reset-password"];

/**
 * The transition a navigate() call plays: `from` is the router's full current path, `target` the path given to
 * navigate (absolute without the basename, relative to the current path, or empty for a search-only change).
 */
export function navigationTransition(from: string, target: string, basename: string): "guest" | "route" | null {
  const base = basename === "/" ? "" : basename;
  const path = !target ? from : target.startsWith("/") ? base + new URL(target, "http://x").pathname : new URL(target, `http://x${from}`).pathname;
  return routeTransition(from, path, base);
}

/**
 * Which view transition a path change plays, or null for none: entering or leaving a guest screen fades the whole
 * page; a sibling tab slides its body (Page.tsx, no snapshot); any other path change cross-fades the content area.
 * Paths are full URL paths (with the router's basename), as router state and links carry them.
 */
export function routeTransition(from: string, to: string, basename = ""): "guest" | "route" | null {
  if (from === to || !canTransition()) return null;
  const guest = (path: string) => guestPaths.some((page) => path === basename + page);
  if (guest(from) || guest(to)) return "guest";
  const tabs = [...document.querySelectorAll<HTMLAnchorElement>(".page-tabs a")].map((link) => link.pathname);
  return tabs.includes(from) && tabs.includes(to) ? null : "route";
}
