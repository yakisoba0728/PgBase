import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";

export type AdminRoute =
  | { kind: "collections" }
  | { kind: "collection-create" }
  | { kind: "collection-edit"; collection: string }
  | { kind: "records"; collection: string }
  | { kind: "record"; collection: string; id?: string }
  | { kind: "storage" }
  | { kind: "backup-storage" }
  | { kind: "operations" }
	| { kind: "sql-console" }
  | { kind: "system" }
  | { kind: "users" }
  | { kind: "auth-collections" }
  | { kind: "service-tokens" }
  | { kind: "schema-transfer" };


type SessionScreen = "setup" | "login" | "shell";

export interface AdminHistory {
  path(): string;
  push(path: string): void;
  replace(path: string): void;
  // `popped` marks a move the browser made on its own. It is the only kind of
  // change the address has already taken before this application sees it, and
  // so the only kind a guard can still refuse.
  listen(onChange: (popped: boolean) => void): () => void;
}

interface AdminLocation {
  route: AdminRoute;
  identity: string;
  navigate(route: AdminRoute): void;
}

const collectionsPath = "/admin/collections";
const operationsPath = "/admin/schema-operations";
const authPaths = { setup: "/admin/setup", login: "/admin/login" } as const;
const editPattern = /^\/admin\/collections\/([^/]+)\/edit$/;
const recordPattern = /^\/admin\/collections\/([^/]+)\/records(?:\/([^/]+))?$/;

// A segment the browser cannot decode names no resource, so the route falls
// back to the collection list rather than addressing a half-decoded name.
function segment(value: string): string | null {
  try { return decodeURIComponent(value); } catch { return null; }
}

export function parseAdminRoute(path: string): AdminRoute {
  if (path === `${collectionsPath}/new`) return { kind: "collection-create" };
  if (path === "/admin/storage/backups") return { kind: "backup-storage" };
  if (path === "/admin/storage") return { kind: "storage" };
  if (path === operationsPath) return { kind: "operations" };
	if (path === "/admin/sql") return { kind: "sql-console" };
	if (path === "/admin/system") return { kind: "system" };
	if (path === "/admin/users") return { kind: "users" };
  if (path === "/admin/auth-collections") return { kind: "auth-collections" };
	if (path === "/admin/service-tokens") return { kind: "service-tokens" };
	if (path === "/admin/schema-transfer") return { kind: "schema-transfer" };
  const edit = editPattern.exec(path);
  if (edit) {
    const collection = segment(edit[1]);
    if (collection !== null) return { kind: "collection-edit", collection };
  }
  const record = recordPattern.exec(path);
  if (record) {
    const collection = segment(record[1]);
    if (collection !== null) {
      if (record[2] === undefined) return { kind: "records", collection };
      if (record[2] === "new") return { kind: "record", collection };
      const id = segment(record[2]);
      if (id !== null) return { kind: "record", collection, id };
    }
  }
  return { kind: "collections" };
}

export function adminRoutePath(route: AdminRoute): string {
  if (route.kind === "collections") return collectionsPath;
  if (route.kind === "collection-create") return `${collectionsPath}/new`;
  if (route.kind === "backup-storage") return "/admin/storage/backups";
  if (route.kind === "storage") return "/admin/storage";
  if (route.kind === "operations") return operationsPath;
	if (route.kind === "sql-console") return "/admin/sql";
	if (route.kind === "system") return "/admin/system";
	if (route.kind === "users") return "/admin/users";
  if (route.kind === "auth-collections") return "/admin/auth-collections";
  if (route.kind === "service-tokens") return "/admin/service-tokens";
  if (route.kind === "schema-transfer") return "/admin/schema-transfer";
  const base = `${collectionsPath}/${encodeURIComponent(route.collection)}`;
  if (route.kind === "collection-edit") return `${base}/edit`;
  if (route.kind === "records") return `${base}/records`;
  return `${base}/records/${route.id === undefined ? "new" : encodeURIComponent(route.id)}`;
}

// NUL separates the parts and an id carries a marker, because a collection
// name or record id may contain every other character: a record literally named
// `new` must never answer to the identity of the create route.
export function adminRouteIdentity(route: AdminRoute): string {
  if (route.kind === "collection-edit" || route.kind === "records") return `${route.kind}\0${route.collection}`;
  if (route.kind === "record") return `record\0${route.collection}\0${route.id === undefined ? "" : `id:${route.id}`}`;
  return route.kind;
}

export function canonicalAdminPath(screen: SessionScreen, path: string): string {
  if (screen !== "shell") return authPaths[screen];
  const onAuthPath = path === authPaths.login || path === authPaths.setup;
  return path.startsWith("/admin/") && !onAuthPath ? path : collectionsPath;
}

function browserAdapter(): AdminHistory {
  const listeners = new Set<(popped: boolean) => void>();
  const announce = (popped: boolean) => { for (const listener of [...listeners]) listener(popped); };
  return {
    path: () => window.location.pathname,
    push: (path) => { window.history.pushState(null, "", path); announce(false); },
    replace: (path) => { window.history.replaceState(null, "", path); announce(false); },
    listen: (onChange) => {
      listeners.add(onChange);
      const popped = () => onChange(true);
      window.addEventListener("popstate", popped);
      return () => { listeners.delete(onChange); window.removeEventListener("popstate", popped); };
    },
  };
}

export const browserHistory = browserAdapter();

export function useAdminLocation(history: AdminHistory = browserHistory, mayLeave: () => boolean = () => true): AdminLocation {
  const leaving = useRef(mayLeave);
  leaving.current = mayLeave;
  const held = useRef(history.path());

  // A refused browser move has already changed the address, so pushing the held
  // path back is the only way to restore it; that push announces itself and is
  // what tells React the route did not move.
  const subscribe = useCallback((onChange: () => void) => history.listen((popped) => {
    const path = history.path();
    // Different URL encodings can still name the same mounted resource. They
    // do not discard its draft, so they must not release its departure guard.
    const sameResource = adminRouteIdentity(parseAdminRoute(path)) === adminRouteIdentity(parseAdminRoute(held.current));
    if (!popped || sameResource || leaving.current()) {
      held.current = path;
      onChange();
      return;
    }
    history.push(held.current);
  }), [history]);

  const path = useSyncExternalStore(subscribe, history.path);
  const navigate = useCallback((route: AdminRoute) => {
    if (adminRouteIdentity(route) === adminRouteIdentity(parseAdminRoute(history.path()))) return;
    if (leaving.current()) history.push(adminRoutePath(route));
  }, [history]);
  const route = parseAdminRoute(path);
  return { route, identity: adminRouteIdentity(route), navigate };
}

// A request that finishes after the administrator moved on must not steer the
// resource now on screen, so a guard captured with the request goes false as
// soon as this outlet is reused for another identity or unmounted.
export function useRouteGuard(identity: string): () => boolean {
  const active = useRef<string | null>(identity);
  active.current = identity;
  useEffect(() => () => { active.current = null; }, []);
  return useCallback(() => active.current === identity, [identity]);
}

type ResourcePatch<S> = Partial<S> | ((current: S) => Partial<S>);

/**
 * State that belongs to one route identity. Changing identity rebuilds it
 * during render rather than a frame later, and an update issued for an identity
 * this component has left is dropped, so a late response can never merge into
 * another resource's snapshot.
 */
export function useResourceState<S extends { identity: string }>(
  identity: string,
  create: (identity: string) => S,
): [S, (issued: string, patch: ResourcePatch<S>) => void] {
  const [state, setState] = useState(() => create(identity));
  const live = state.identity === identity ? state : create(identity);
  if (live !== state) setState(live);
  const update = useCallback((issued: string, patch: ResourcePatch<S>) => {
    setState((current) => current.identity !== issued
      ? current
      : { ...current, ...(typeof patch === "function" ? patch(current) : patch) });
  }, []);
  return [live, update];
}
