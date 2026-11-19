import { createElement, lazy, type ComponentType } from "react";

// Screen chunk loaders by route path prefix (no leading slash); hovering or focusing a link to one starts its download.
const loaders: [path: string, run: () => Promise<unknown>][] = [];

/**
 * A screen in its own chunk, loaded on first visit or earlier by preloadScreen. Once loaded it renders directly, so a
 * preloaded screen never shows the shell's Suspense fallback.
 */
export function screen<M>(path: string, load: () => Promise<M>, name: keyof M): ComponentType {
  let loaded: ComponentType | undefined;
  let loading: Promise<ComponentType> | undefined;
  // One request per chunk however often it is asked for; a failed one may be asked for again.
  const run = () => (loading ??= load().then(
    (module) => (loaded = module[name] as ComponentType),
    (error: unknown) => {
      loading = undefined;
      throw error;
    },
  ));
  loaders.push([path, run]);
  const Lazy = lazy(() => run().then((component) => ({ default: component })));
  return function Screen() {
    return createElement(loaded ?? Lazy);
  };
}

/** Starts loading the screen chunk behind an app path ("/logs", "/collections/posts/rules"); a failure is left to the visit. */
export function preloadScreen(pathname: string) {
  const path = pathname.replace(/^\/+/, "");
  for (const [route, run] of loaders) {
    if (path === route || path.startsWith(`${route}/`)) void run().catch(() => {});
  }
}
