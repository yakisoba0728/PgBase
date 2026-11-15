import { useContext } from "react";
import { Link, useLocation, useMatch } from "react-router";
import { QueryClientContext } from "@tanstack/react-query";
import { collectionsQuery } from "../collections/queries";
import { navGroups, pageTabs, screenTabs } from "../app/navigation";
import { useT } from "../i18n";
import { Page } from "./Page";
import { Skeleton, type SkeletonVariant } from "./Feedback";

/** The shell and destination title stay usable while a lazy page or catalog loads; the body takes the shape the screen's own skeleton will. */
export function PageSkeleton() {
  const t = useT();
  const { pathname } = useLocation();
  // Only peeked at (the navigation loads it): the collection's type is the crumb its page will show.
  const catalog = useContext(QueryClientContext)?.getQueryData(collectionsQuery.queryKey);
  const collection = useMatch("/collections/:name/:tab?");
  const authCollection = useMatch("/auth/collections/:name/:tab?");
  const entry = navGroups.flatMap((group) => group.items).find(({ path }) => pathname === `/${path}` || pathname.startsWith(`/${path}/`));
  const name = collection?.params.name ?? authCollection?.params.name;
  let title = entry ? t(entry.label) : t("common.loading");
  if (name) title = name;
  else if (pathname === "/collections") title = t("nav.collections");
  else if (pathname.startsWith("/new-collection")) title = t("collections.new");
  else if (pathname === "/profile") title = t("account.profile");
  let variant: SkeletonVariant = "table";
  if (authCollection || collection?.params.tab || /^\/(new-collection|storage)(\/|$)/.test(pathname)) variant = "form";
  if (/^\/(settings|profile)(\/|$)/.test(pathname)) variant = "sections";
  if (pathname === "/sql" || collection?.params.tab === "api" || pathname.startsWith("/schema-transfer")) variant = "editor";
  if (pathname === "/realtime") variant = "cards";
  const found = collection && Object.values(catalog?.collections ?? {}).find((candidate) => candidate.name === name);
  const crumb = found ? t(`collections.type.${found.type ?? "base"}`) : authCollection && name ? <Link to="/auth/collections">{t("nav.authCollections")}</Link> : undefined;
  const tabGroup = pathname.startsWith("/settings") ? "settings" : pathname.startsWith("/storage") ? "storage" : pathname.startsWith("/schema-transfer") ? "schemaTransfer" : undefined;
  let tabs = tabGroup ? pageTabs(t, screenTabs[tabGroup]) : undefined;
  if (collection && name) {
    const base = `/collections/${encodeURIComponent(name)}`;
    tabs = (["records", "schema", "rules", "api"] as const).map((tab) => ({
      to: tab === "records" ? base : `${base}/${tab}`, label: t(`tab.${tab}`), end: tab === "records",
    }));
  } else if (authCollection && name) {
    const base = `/auth/collections/${encodeURIComponent(name)}`;
    tabs = (["policy", "providers", "tokens", "mail"] as const).map((tab) => ({
      to: tab === "policy" ? base : `${base}/${tab}`, label: t(`auth.tab.${tab}`), end: tab === "policy",
    }));
  }
  return (
    <Page title={title} crumb={crumb} tabs={tabs}>
      <div className="page-skeleton">
        {/* LogStats' own loading state, above the list. */}
        {pathname === "/logs" && <div className="ops-stats" aria-hidden="true"><Skeleton variant="cards" /></div>}
        {/* Lists without a toolbar: one straight in a page-content, two straight under the header. */}
        {pathname === "/auth/collections" ? (
          <div className="page-content"><Skeleton variant={variant} /></div>
        ) : (
          <Skeleton variant={variant} className={pathname === "/service-tokens" || pathname === "/administrators" ? "skeleton-bare" : undefined} />
        )}
      </div>
    </Page>
  );
}
