import { Navigate, useParams } from "react-router";
import { RiDatabase2Line, RiErrorWarningLine, RiToolsLine } from "@remixicon/react";
import { errorText } from "../app/queries";
import { RecordsActions, RecordsView } from "../collections/RecordsView";
import { useCollection } from "../collections/queries";
import { SchemaView } from "../schema/SchemaView";
import { EmptyState, Loader } from "../components/Feedback";
import { Page } from "../components/Page";
import { useT } from "../i18n";

const subTabs = ["schema", "rules", "api"] as const;

/** /collections/:name/:tab? — Records by default; Rules and API are later phases. */
export function CollectionPage() {
  const t = useT();
  const { name = "", tab } = useParams();
  const { collection, data, isPending, error } = useCollection(name);
  if (tab && !subTabs.includes(tab as (typeof subTabs)[number])) return <Navigate to={`/collections/${encodeURIComponent(name)}`} replace />;
  if (isPending) return <Loader />;
  if (!collection) {
    return (
      <Page title={name}>
        <EmptyState icon={RiErrorWarningLine}>{error ? errorText(t, error) : t("collections.missing")}</EmptyState>
      </Page>
    );
  }
  const base = `/collections/${encodeURIComponent(collection.name)}`;
  const tabs = [
    { to: base, label: t("tab.records"), end: true },
    { to: `${base}/schema`, label: t("tab.schema") },
    { to: `${base}/rules`, label: t("tab.rules") },
    { to: `${base}/api`, label: t("tab.api") },
  ];
  return (
    <Page title={collection.name} crumb={t(`collections.type.${collection.type ?? "base"}`)} tabs={tabs} actions={!tab && <RecordsActions collection={collection} />}>
      {tab === "schema" ? (
        <SchemaView key={collection.id} collection={collection} type={collection.type ?? "base"} catalog={data ?? []} basePath={base} />
      ) : tab ? (
        <EmptyState icon={RiToolsLine}>{t("placeholder.empty")}</EmptyState>
      ) : (
        <RecordsView key={collection.name} collection={collection} />
      )}
    </Page>
  );
}

/** /collections: opens the first collection, or says there are none. */
export function CollectionsIndexPage() {
  const t = useT();
  const { data, isPending, error } = useCollection("");
  if (isPending) return <Loader />;
  const first = data?.[0];
  if (first) return <Navigate to={`/collections/${encodeURIComponent(first.name)}`} replace />;
  return (
    <Page title={t("nav.collections")}>
      <EmptyState icon={RiDatabase2Line}>{error ? errorText(t, error) : t("collections.empty")}</EmptyState>
    </Page>
  );
}
