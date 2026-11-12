import { useEffect, useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { adminRouteIdentity, type AdminRoute } from "../routing/admin-location";
import { AdminLink } from "../routing/admin-link";
import type { AdminExtensionScreen } from "../extensions/admin-extensions";
import { WorkspaceIcon, type WorkspaceIconName } from "./workspace-icon";
import { Button } from "./ui";
import { routeAvailable } from "../backend/capabilities";
import { useBackend } from "../auth/session";
import type { CollectionDefinition } from "../backend/ports";
import { t, useLocale } from "../i18n";
import { LanguagePicker } from "../i18n/language-picker";

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
  useLocale();
  const backend = useBackend();
  const available = (item: NavigationItem) => routeAvailable(backend, item.route);
  return navigationGroups.map(group => ({ ...group, items: group.items.flatMap(item => available(item) ? [{ ...item, children: item.children?.filter(available) }] : (item.children ?? []).filter(available)) })).filter(group => group.items.length);
}
export function workspaceLabel(route: AdminRoute) {
  if (route.kind === "collection-create") return t("Create collection");
  if (route.kind === "collection-scaffold") return t("Create {type} collection", { type: t(route.scaffold) });
  if (route.kind === "collection-duplicate") return t("Duplicate collection");
  if (route.kind === "collection-edit") return t("Schema");
  if (route.kind === "records") return t("Records");
  if (route.kind === "logs") return t("Logs");
  if (route.kind === "record") return t(route.id === undefined ? "Create record" : "Edit record");
  return t(allNavigationItems.find(item => item.route.kind === route.kind)?.label ?? (route.kind === "sql-console" ? "SQL console" : "Extensions"));
}
function workspaceGroup(route: AdminRoute) {
  const parent = navigationParent(route);
  return navigationGroups.find(group => group.items.some(item => item === parent))?.label ?? "Tools";
}
export function WorkspaceHeader({ email, route, navigationOpen, pending, onToggleNavigation, onSignOut, toggleRef, navigate }: {
  email: string; route: AdminRoute; navigationOpen: boolean; pending: boolean;
  onToggleNavigation(): void; onSignOut(): void; toggleRef: RefObject<HTMLButtonElement | null>; navigate(route: AdminRoute): void;
}) {
  useLocale();
  const backend = useBackend();
  const collection = "collection" in route ? route.collection : undefined;
  const parent = navigationParent(route);
  const secondary = parent?.children?.some(item => itemMatchesRoute(item, route));
  return <header className="workspace-header" inert={navigationOpen}>
    <div className="workspace-brand"><h1 aria-label={t("pgBase administration")}>pgBase</h1></div>
    <div className="workspace-location">
      <button ref={toggleRef} aria-controls="workspace-navigation" aria-expanded={navigationOpen} aria-label={t("Toggle navigation")} className="icon-button navigation-toggle" disabled={pending} onClick={onToggleNavigation} type="button"><WorkspaceIcon name="menu" /></button>
      <nav className="workspace-breadcrumbs" aria-label={t("Breadcrumb")}>
        <span className="workspace-context">{t(workspaceGroup(route))}</span><span aria-hidden="true" className="breadcrumb-divider">/</span>
        {collectionKinds.has(route.kind) && route.kind !== "collections" && <><AdminLink className="breadcrumb-collections-overview" ariaLabel={t("Open collections overview")} navigate={navigate} route={{ kind: "collections" }}>{t("Collections")}</AdminLink><span aria-hidden="true" className="breadcrumb-divider">/</span></>}
        {secondary && parent && routeAvailable(backend, parent.route) && <><AdminLink ariaLabel={t("Open {name} overview", { name: t(parent.label.toLowerCase()) })} navigate={navigate} route={parent.route}>{t(parent.label)}</AdminLink><span aria-hidden="true" className="breadcrumb-divider">/</span></>}
        {collection && <><AdminLink className="breadcrumb-resource" ariaLabel={t("Open {name} records", { name: collection })} navigate={navigate} route={{ kind: "records", collection }}>{collection}</AdminLink><span aria-hidden="true" className="breadcrumb-divider">/</span></>}
        <strong aria-current="page">{workspaceLabel(route)}</strong>
      </nav>
    </div>
    <div className="workspace-account"><LanguagePicker /><span className="account-email">{email}</span><Button variant="secondary" className="sign-out" disabled={pending} onClick={onSignOut}>{t(pending ? "Signing out…" : "Sign out")}</Button></div>
  </header>;
}
export function WorkspaceNavigation({ route, open, navigate, onClose, extensions, sqlEnabled }: {
  route: AdminRoute; open: boolean; navigate(route: AdminRoute): void; onClose(): void;
  extensions: readonly AdminExtensionScreen[]; sqlEnabled: boolean;
}) {
  useLocale();
  const backend = useBackend();
  const [query, setQuery] = useState("");
  const [collections, setCollections] = useState<CollectionDefinition[] | null>(null);
  const [catalogFailed, setCatalogFailed] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const root = useRef<HTMLElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const identity = adminRouteIdentity(route);
  useEffect(() => setQuery(""), [identity]);
  useEffect(() => {
    const controller = new AbortController();
    setCatalogFailed(false);
    void backend.schema.catalog(controller.signal).then(catalog => {
      if (!controller.signal.aborted) setCollections(catalog.all());
    }).catch(() => { if (!controller.signal.aborted) setCatalogFailed(true); });
    return () => controller.abort();
  }, [backend.schema, identity, refresh]);
  useEffect(() => { if (open) search.current?.focus(); }, [open]);
  const groups = useNavigationGroups();
  const tools: NavigationItem[] = extensions.map(screen => ({ label: screen.label, route: { kind: "extension", id: screen.id, path: screen.path }, icon: "layers" }));
  if (sqlEnabled) tools.push({ label: "SQL console", route: { kind: "sql-console" }, icon: "terminal" });
  if (tools.length) {
    const utility = groups.findIndex(group => group.label === "Workspace");
    groups.splice(utility < 0 ? groups.length : utility, 0, { label: "Tools", items: tools });
  }
  const term = query.trim().toLowerCase();
  const visible = groups.map(group => ({ ...group, items: group.items.flatMap(item => term ? [item, ...(item.children ?? [])].filter(page => `${group.label} ${t(group.label)} ${page.label} ${page.route.kind === "extension" ? page.label : t(page.label)} ${page.keywords ?? ""}`.toLowerCase().includes(term)) : [item, ...(item.children ?? [])]) })).filter(group => group.items.length);
  const active = (item: NavigationItem) => itemMatchesRoute(item, route) || (!term && item.children?.some(child => itemMatchesRoute(child, route))) || false;
  const renderGroup = (group: typeof navigationGroups[number]) => <div className={`navigation-section${group.label === "Workspace" ? " navigation-utility" : ""}`} key={group.label}>
    <h2 className="navigation-label">{t(group.label)}</h2>
    <ul aria-label={t(group.label)} className="navigation-group">{group.items.map(item => <li key={adminRouteIdentity(item.route)}><AdminLink className="navigation-link" current={active(item)} navigate={navigate} route={item.route}><WorkspaceIcon name={item.icon} /><span>{item.route.kind === "extension" ? item.label : t(item.label)}</span></AdminLink></li>)}</ul>
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
  const railItems: NavigationItem[] = [
    { label: "Collections", route: { kind: "collections" }, icon: "database" },
    { label: "Logs", route: { kind: "logs" }, icon: "activity" },
    { label: "Settings", route: { kind: "settings" }, icon: "settings" },
  ] satisfies NavigationItem[];
  const availableRailItems = railItems.filter(item => routeAvailable(backend, item.route));
  const collectionContext = collectionKinds.has(route.kind);
  const listed = collections?.filter(collection => collection.name.toLowerCase().includes(term)) ?? [];
  const resource = "collection" in route ? route.collection : undefined;
  return <>
    {open && <div className="navigation-backdrop" onClick={onClose} aria-hidden="true" />}
    <aside ref={root} onKeyDown={handleKey} role={open ? "dialog" : undefined} aria-modal={open ? true : undefined} aria-label={open ? t("Workspace navigation") : undefined} className={`workspace-sidebar${open ? " is-open" : ""}`} id="workspace-navigation">
      <nav className="workspace-rail" aria-label={t("Primary sections")}>
        <span className="rail-brand" aria-hidden="true"><WorkspaceIcon name="database" /></span>
        {availableRailItems.map(item => <AdminLink key={item.label} className="rail-link" ariaLabel={t("{name} section", { name: t(item.label) })} current={item.route.kind === "collections" ? collectionContext : item.route.kind === "logs" ? route.kind === "logs" : !collectionContext && route.kind !== "logs"} navigate={navigate} route={item.route}><WorkspaceIcon name={item.icon} /><span>{item.route.kind === "extension" ? item.label : t(item.label)}</span></AdminLink>)}
      </nav>
      <div className="workspace-context-sidebar">
      <div className="navigation-drawer-heading"><strong>{t("Navigation")}</strong><Button variant="secondary" onClick={onClose}>{t("Close navigation")}</Button></div>
      <div className="sidebar-heading"><strong>{t(collectionContext ? "Collections" : workspaceGroup(route) === "Operations" ? "Operations" : "Project settings")}</strong></div>
      <label className="navigation-search"><WorkspaceIcon name="search" /><input ref={search} aria-label={t("Find a page")} type="search" value={query} placeholder={t(collectionContext ? "Search collections or pages…" : "Find a page…")} onChange={event => setQuery(event.target.value)} />{term && <Button variant="ghost" onClick={() => { setQuery(""); search.current?.focus(); }}>{t("Clear")}</Button>}</label>
      {collectionContext && <div className="sidebar-collections">
        <nav aria-label={t("Project collections")}><ul className="collection-navigation-list">{listed.map(collection => <li key={collection.id ?? collection.name}><AdminLink className="navigation-link collection-navigation-link" ariaLabel={t("Open {name} collection", { name: collection.name })} current={collection.name === resource} route={{ kind: "records", collection: collection.name }} navigate={navigate}><WorkspaceIcon name={collection.view ? "layers" : collection.type === "auth" ? "shield" : "database"} /><span>{collection.name}</span><span className="collection-navigation-type">{collection.view ? t("view") : collection.type === "auth" ? t("auth") : ""}</span></AdminLink></li>)}</ul></nav>
        {collections === null && !catalogFailed && <p className="sidebar-message" role="status">{t("Loading collection navigation…")}</p>}
        {catalogFailed && <p className="sidebar-message">{t("Unable to load collections.")} <Button variant="ghost" onClick={() => setRefresh(value => value + 1)}>{t("Retry collection navigation")}</Button></p>}
        {collections && listed.length === 0 && <p className="sidebar-message">{t(term ? "No matching collections." : "Create a collection to get started.")}</p>}
        <AdminLink className="sidebar-create-collection" ariaLabel={t("New collection")} route={{ kind: "collection-create" }} navigate={navigate}><WorkspaceIcon name="plus" />{t("New collection")}</AdminLink>
      </div>}
      <nav aria-label={t("Administration")}>{collectionContext && !term && routeAvailable(backend, { kind: "settings" }) ? <div className="sidebar-utilities"><AdminLink className="navigation-link" route={{ kind: "settings" }} navigate={navigate}><WorkspaceIcon name="settings" />{t("Settings")}</AdminLink></div> : <><div className="navigation-main">{visible.filter(group => group.label !== "Workspace").map(renderGroup)}</div>{visible.filter(group => group.label === "Workspace").map(renderGroup)}</>}</nav>
      {term && <p className="navigation-results" role="status">{t("{count} pages match “{query}”.", { count: visible.reduce((count, group) => count + group.items.length, 0), query })}</p>}
      </div>
    </aside>
  </>;
}
export function WorkspaceContextNavigation({ route, navigate }: { route: AdminRoute; navigate(route: AdminRoute): void }) {
  useLocale();
  const backend = useBackend();
  if (route.kind === "records") return null;
  if (route.kind === "record" || route.kind === "collection-edit") return <nav className="context-tabs" aria-label={t("Collection workspace")}>
    <AdminLink navigate={navigate} route={{ kind: "records", collection: route.collection }} current={route.kind !== "collection-edit"}>{t("Records")}</AdminLink>
    <AdminLink navigate={navigate} route={{ kind: "collection-edit", collection: route.collection }} current={route.kind === "collection-edit"}>{t("Schema")}</AdminLink>
  </nav>;
  const parent = navigationParent(route);
  const section = parent;
  const items = section ? [section, ...(section.children ?? [])].filter(item => routeAvailable(backend, item.route)) : [];
  if (!section || items.length < 2) return null;
  return <nav className="context-tabs" aria-label={t("{name} workspace", { name: t(section.label) })}>
    {items.map(item => <AdminLink key={adminRouteIdentity(item.route)} ariaLabel={item === section ? t("Open {name} section", { name: t(item.label.toLowerCase()) }) : undefined} navigate={navigate} route={item.route} current={itemMatchesRoute(item, route)}>{item.route.kind === "extension" ? item.label : t(item.label)}</AdminLink>)}
  </nav>;
}
