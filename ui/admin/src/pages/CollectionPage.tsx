import { Navigate, useParams } from "react-router";
import { RiDatabase2Line, RiErrorWarningLine } from "@remixicon/react";
import { RecordsActions, RecordsView } from "../collections/RecordsView";
import { ApiView } from "../apidocs/ApiView";
import { RulesView } from "../rules/RulesView";
import { useCollection } from "../collections/queries";
import { SchemaView } from "../schema/SchemaView";
import { SchemaDraftGuard } from "../schema/DraftGuard";
import { EmptyState, ErrorState } from "../components/Feedback";
import { Page } from "../components/Page";
import { PageSkeleton } from "../components/PageSkeleton";
import { useT } from "../i18n";
import { useCollectionVisit, useRecentCollections } from "../app/recent";

const subTabs = ["schema", "rules", "api"] as const;

/** /collections/:name/:tab? — Records by default. */
export function CollectionPage() {
  const t = useT();
  const { name = "", tab } = useParams();
  const { collection, data, isPending, error, refetch } = useCollection(name);
  useCollectionVisit(collection ? collection.name : "", tab);
  if (tab && !subTabs.includes(tab as (typeof subTabs)[number])) return <Navigate to={`/collections/${encodeURIComponent(name)}`} replace />;
  if (isPending) return <PageSkeleton />;
  if (!collection) {
    return (
      <Page title={name}>
        {error ? <ErrorState error={error} onRetry={refetch} /> : <EmptyState icon={RiErrorWarningLine}>{t("collections.missing")}</EmptyState>}
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
      ) : tab === "rules" ? (
        <RulesView key={collection.name} collection={collection} basePath={base} />
      ) : tab === "api" ? (
        <ApiView key={collection.name} collection={collection} />
      ) : (
        <RecordsView key={collection.name} collection={collection} />
      )}
      {tab !== "schema" && <SchemaDraftGuard draftKey={collection.id} basePath={base} />}
    </Page>
  );
}

/** /collections: opens the first collection, or says there are none. */
export function CollectionsIndexPage() {
  const t = useT();
  const { data, isPending, error, refetch } = useCollection("");
  const recent = useRecentCollections(data?.map((collection) => collection.name));
  if (isPending) return <PageSkeleton />;
  // The most recently opened collection, else the first one.
  const first = recent[0] ? { name: recent[0] } : data?.[0];
  if (first) return <Navigate to={`/collections/${encodeURIComponent(first.name)}`} replace />;
  return (
    <Page title={t("nav.collections")}>
      {error ? <ErrorState error={error} onRetry={refetch} /> : <EmptyState icon={RiDatabase2Line}>{t("collections.empty")}</EmptyState>}
    </Page>
  );
}
