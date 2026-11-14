import { useLocation, useMatch } from "react-router";
import { navGroups, pageTabs, screenTabs } from "../app/navigation";
import { useT } from "../i18n";
import { Page } from "./Page";
import { Skeleton, type SkeletonVariant } from "./Feedback";

/** The shell and destination title stay usable while a lazy page or catalog loads. */
export function PageSkeleton() {
  const t = useT();
  const { pathname } = useLocation();
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
  if (authCollection || collection?.params.tab || /^\/(new-collection|settings|storage|profile|schema-transfer)(\/|$)/.test(pathname)) variant = "form";
  if (pathname === "/sql" || collection?.params.tab === "api") variant = "editor";
  if (pathname === "/realtime" || pathname === "/settings/status") variant = "cards";
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
    <Page title={title} tabs={tabs}>
      <div className="page-skeleton">
        {pathname === "/logs" && <div aria-hidden="true"><Skeleton variant="cards" /></div>}
        <Skeleton variant={variant} />
        {pathname === "/realtime" && <div aria-hidden="true"><Skeleton /></div>}
      </div>
    </Page>
  );
}
