import { useEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { adminRouteIdentity, type AdminRoute } from "../routing/admin-location";
import { AdminLink } from "../routing/admin-link";
import type { AdminExtensionScreen } from "../extensions/admin-extensions";
import { WorkspaceIcon, type WorkspaceIconName } from "./workspace-icon";
import { Button } from "./ui";
import { routeAvailable } from "../backend/capabilities";
import { useBackend } from "../auth/session";

type NavigationItem = { label: string; route: AdminRoute; icon: WorkspaceIconName; keywords?: string; children?: NavigationItem[] };
const navigationGroups: { label: string; items: NavigationItem[] }[] = [
  { label: "Data", items: [
    { label: "Collections", route: { kind: "collections" }, icon: "database", keywords: "records schema tables", children: [
      { label: "Schema transfer", route: { kind: "schema-transfer" }, icon: "transfer", keywords: "import export" },
    ] },
    { label: "File storage", route: { kind: "storage" }, icon: "folder", keywords: "uploads s3 files", children: [
      { label: "Backup storage", route: { kind: "backup-storage" }, icon: "backup", keywords: "archives s3" },
    ] },
  ] },
  { label: "Access", items: [
    { label: "Users", route: { kind: "users" }, icon: "users", keywords: "accounts sessions", children: [
      { label: "Auth collections", route: { kind: "auth-collections" }, icon: "shield", keywords: "authentication login registration" },
      { label: "Administrators", route: { kind: "administrators" }, icon: "shield" },
      { label: "Service tokens", route: { kind: "service-tokens" }, icon: "key", keywords: "api keys" },
    ] },
  ] },
  { label: "Operations", items: [
    { label: "System", route: { kind: "system" }, icon: "activity", keywords: "status health logs requests jobs", children: [
      { label: "Schema operations", route: { kind: "operations" }, icon: "layers", keywords: "history migrations" },
    ] },
  ] },
  { label: "Workspace", items: [
    { label: "Settings", route: { kind: "settings" }, icon: "settings", keywords: "appearance theme profile backups network" },
  ] },
];
const collectionKinds = new Set(["collections", "collection-create", "collection-edit", "collection-duplicate", "collection-scaffold", "records", "record"]);
const allNavigationItems = navigationGroups.flatMap(group => group.items.flatMap(item => [item, ...(item.children ?? [])]));
function itemMatchesRoute(item: NavigationItem, route: AdminRoute): boolean {
  if (item.route.kind === "collections") return collectionKinds.has(route.kind);
  if (item.route.kind === "extension") return route.kind === "extension" && item.route.id === route.id;
  return item.route.kind === route.kind;
}
function navigationParent(route: AdminRoute) {
  return navigationGroups.flatMap(group => group.items).find(item => itemMatchesRoute(item, route) || item.children?.some(child => itemMatchesRoute(child, route)));
}
function useNavigationGroups(): typeof navigationGroups {
  const backend = useBackend();
  const available = (item: NavigationItem) => routeAvailable(backend, item.route);
  return navigationGroups.map(group => ({ ...group, items: group.items.flatMap(item => available(item) ? [{ ...item, children: item.children?.filter(available) }] : (item.children ?? []).filter(available)) })).filter(group => group.items.length);
}
export function workspaceLabel(route: AdminRoute) {
  if (route.kind === "collection-create") return "Create collection";
  if (route.kind === "collection-scaffold") return `Create ${route.scaffold} collection`;
  if (route.kind === "collection-duplicate") return "Duplicate collection";
  if (route.kind === "collection-edit") return "Schema";
  if (route.kind === "records") return "Records";
  if (route.kind === "record") return route.id === undefined ? "Create record" : "Edit record";
  return allNavigationItems.find(item => item.route.kind === route.kind)?.label ?? (route.kind === "sql-console" ? "SQL console" : "Extensions");
}
function workspaceGroup(route: AdminRoute) {
  const parent = navigationParent(route);
  return navigationGroups.find(group => group.items.some(item => item === parent))?.label ?? "Tools";
}
export function WorkspaceHeader({ email, route, navigationOpen, pending, onToggleNavigation, onSignOut, toggleRef, navigate }: {
  email: string; route: AdminRoute; navigationOpen: boolean; pending: boolean;
  onToggleNavigation(): void; onSignOut(): void; toggleRef: RefObject<HTMLButtonElement | null>; navigate(route: AdminRoute): void;
}) {
  const backend = useBackend();
  const collection = "collection" in route ? route.collection : undefined;
  const parent = navigationParent(route);
  const secondary = parent?.children?.some(item => itemMatchesRoute(item, route));
  return <header className="workspace-header" inert={navigationOpen}>
    <div className="workspace-brand"><h1 aria-label="pgBase administration">pgBase</h1></div>
    <div className="workspace-location">
      <button ref={toggleRef} aria-controls="workspace-navigation" aria-expanded={navigationOpen} aria-label="Toggle navigation" className="icon-button navigation-toggle" disabled={pending} onClick={onToggleNavigation} type="button"><WorkspaceIcon name="menu" /></button>
      <nav className="workspace-breadcrumbs" aria-label="Breadcrumb">
        <span className="workspace-context">{workspaceGroup(route)}</span><span aria-hidden="true" className="breadcrumb-divider">/</span>
        {collectionKinds.has(route.kind) && route.kind !== "collections" && <><AdminLink ariaLabel="Open collections overview" navigate={navigate} route={{ kind: "collections" }}>Collections</AdminLink><span aria-hidden="true" className="breadcrumb-divider">/</span></>}
        {secondary && parent && routeAvailable(backend, parent.route) && <><AdminLink ariaLabel={`Open ${parent.label.toLowerCase()} overview`} navigate={navigate} route={parent.route}>{parent.label}</AdminLink><span aria-hidden="true" className="breadcrumb-divider">/</span></>}
        {collection && <><AdminLink className="breadcrumb-resource" ariaLabel={`Open ${collection} records`} navigate={navigate} route={{ kind: "records", collection }}>{collection}</AdminLink><span aria-hidden="true" className="breadcrumb-divider">/</span></>}
        <strong aria-current="page">{workspaceLabel(route)}</strong>
      </nav>
    </div>
    <div className="workspace-account"><span className="account-email">{email}</span><Button variant="secondary" className="sign-out" disabled={pending} onClick={onSignOut}>{pending ? "Signing out…" : "Sign out"}</Button></div>
  </header>;
}
export function WorkspaceNavigation({ route, open, navigate, onClose, extensions, sqlEnabled }: {
  route: AdminRoute; open: boolean; navigate(route: AdminRoute): void; onClose(): void;
  extensions: readonly AdminExtensionScreen[]; sqlEnabled: boolean;
}) {
  const [query, setQuery] = useState("");
  const root = useRef<HTMLElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const identity = adminRouteIdentity(route);
  useEffect(() => setQuery(""), [identity]);
  useEffect(() => { if (open) search.current?.focus(); }, [open]);
  const groups = useNavigationGroups();
  const tools: NavigationItem[] = extensions.map(screen => ({ label: screen.label, route: { kind: "extension", id: screen.id, path: screen.path }, icon: "layers" }));
  if (sqlEnabled) tools.push({ label: "SQL console", route: { kind: "sql-console" }, icon: "terminal" });
  if (tools.length) {
    const utility = groups.findIndex(group => group.label === "Workspace");
    groups.splice(utility < 0 ? groups.length : utility, 0, { label: "Tools", items: tools });
  }
  const term = query.trim().toLowerCase();
  const visible = groups.map(group => ({ ...group, items: group.items.flatMap(item => term ? [item, ...(item.children ?? [])].filter(page => `${group.label} ${page.label} ${page.keywords ?? ""}`.toLowerCase().includes(term)) : [item]) })).filter(group => group.items.length);
  const active = (item: NavigationItem) => itemMatchesRoute(item, route) || (!term && item.children?.some(child => itemMatchesRoute(child, route))) || false;
  const renderGroup = (group: typeof navigationGroups[number]) => <div className={`navigation-section${group.label === "Workspace" ? " navigation-utility" : ""}`} key={group.label}>
    <h2 className="navigation-label">{group.label}</h2>
    <ul aria-label={group.label} className="navigation-group">{group.items.map(item => <li key={adminRouteIdentity(item.route)}><AdminLink className="navigation-link" current={active(item)} navigate={navigate} route={item.route}><WorkspaceIcon name={item.icon} /><span>{item.label}</span></AdminLink></li>)}</ul>
  </div>;
  function handleKey(event: KeyboardEvent<HTMLElement>) {
    if (!open) return;
    if (event.key === "Escape") { event.preventDefault(); onClose(); return; }
    if (event.key !== "Tab") return;
    const controls = root.current?.querySelectorAll<HTMLElement>('a[href],button:not([disabled]),input:not([disabled]),[tabindex="0"]');
    if (!controls?.length) return;
    const first = controls[0], last = controls[controls.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
  return <>
    {open && <div className="navigation-backdrop" onClick={onClose} aria-hidden="true" />}
    <aside ref={root} onKeyDown={handleKey} role={open ? "dialog" : undefined} aria-modal={open ? true : undefined} aria-label={open ? "Workspace navigation" : undefined} className={`workspace-sidebar${open ? " is-open" : ""}`} id="workspace-navigation">
      <div className="navigation-drawer-heading"><strong>Navigation</strong><Button variant="secondary" onClick={onClose}>Close navigation</Button></div>
      <label className="navigation-search"><WorkspaceIcon name="search" /><input ref={search} aria-label="Find a page" type="search" value={query} placeholder="Find a page…" onChange={event => setQuery(event.target.value)} />{term && <Button variant="ghost" onClick={() => { setQuery(""); search.current?.focus(); }}>Clear</Button>}</label>
      <nav aria-label="Administration"><div className="navigation-main">{visible.filter(group => group.label !== "Workspace").map(renderGroup)}</div>{visible.filter(group => group.label === "Workspace").map(renderGroup)}</nav>
      {term && <p className="navigation-results" role="status">{visible.reduce((count, group) => count + group.items.length, 0)} pages match “{query}”.</p>}
    </aside>
  </>;
}
export function WorkspaceContextNavigation({ route, navigate }: { route: AdminRoute; navigate(route: AdminRoute): void }) {
  const backend = useBackend();
  if (route.kind === "records" || route.kind === "record" || route.kind === "collection-edit") return <nav className="context-tabs" aria-label="Collection workspace">
    <AdminLink navigate={navigate} route={{ kind: "records", collection: route.collection }} current={route.kind !== "collection-edit"}>Records</AdminLink>
    <AdminLink navigate={navigate} route={{ kind: "collection-edit", collection: route.collection }} current={route.kind === "collection-edit"}>Schema</AdminLink>
  </nav>;
  const parent = navigationParent(route);
  const section = parent;
  const items = section ? [section, ...(section.children ?? [])].filter(item => routeAvailable(backend, item.route)) : [];
  if (!section || items.length < 2) return null;
  return <nav className="context-tabs" aria-label={`${section.label} workspace`}>
    {items.map(item => <AdminLink key={adminRouteIdentity(item.route)} ariaLabel={item === section ? `Open ${item.label.toLowerCase()} section` : undefined} navigate={navigate} route={item.route} current={itemMatchesRoute(item, route)}>{item.label}</AdminLink>)}
  </nav>;
}
