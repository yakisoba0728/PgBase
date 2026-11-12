import { useState } from "react";
import { Link, Navigate, useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiErrorWarningLine, RiShieldUserLine } from "@remixicon/react";
import { deleteAuthCollection, type AuthCollection } from "../api/authCollections";
import { ApiError } from "../api/http";
import { errorText } from "../app/queries";
import { collectionsQuery } from "../collections/queries";
import { Button } from "../components/Button";
import { ErrorState } from "../components/ErrorState";
import { TypedConfirmDialog } from "../components/TaskDialog";
import { EmptyState, Loader } from "../components/Feedback";
import { Page } from "../components/Page";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { deleteCollection } from "../schema/apply";
import { MailTab } from "./MailTab";
import { PolicyTab } from "./PolicyTab";
import { ProvidersTab } from "./ProvidersTab";
import { administrationQuery, authCollectionsQuery, tokenPoliciesQuery } from "./queries";
import { TokensTab } from "./TokensTab";
import "../styles/auth.css";

const tabs = ["policy", "providers", "tokens", "mail"] as const;
type Tab = (typeof tabs)[number];
const root = "/auth/collections";
// The built-in "users" collection cannot be deleted (userauth.DefaultCollectionID).
const defaultCollectionId = "00000000-0000-0000-0000-000000000001";

/** /auth/collections: every auth collection; new ones are made by the schema designer. */
export function AuthCollectionsIndex() {
  const t = useT();
  const navigate = useNavigate();
  const query = useQuery(authCollectionsQuery);
  const actions = <Link className="btn btn-sm" to="/new-collection/auth"><RiAddLine size={16} aria-hidden />{t("auth.collections.new")}</Link>;
  return (
    <Page title={t("nav.authCollections")} actions={actions}>
      <div className="page-content">
        {query.isPending ? (
          <Loader />
        ) : query.isError ? (
          <ErrorState error={query.error} busy={query.isFetching} onRetry={() => void query.refetch()} />
        ) : query.data.length === 0 ? (
          <EmptyState icon={RiShieldUserLine}>{t("auth.collections.empty")}</EmptyState>
        ) : (
          <table className="grid auth-table" aria-label={t("nav.authCollections")}>
            <thead>
              <tr>
                <th>{t("auth.collections.name")}</th>
                <th>{t("auth.collections.registration")}</th>
                <th>{t("auth.collections.fields")}</th>
                <th>{t("auth.collections.recordId")}</th>
                <th>id</th>
              </tr>
            </thead>
            <tbody>
              {query.data.map((collection) => (
                <tr key={collection.id} className="auth-link-row" onClick={() => navigate(`${root}/${encodeURIComponent(collection.name)}`)}>
                  <td><Link className="mono" to={`${root}/${encodeURIComponent(collection.name)}`}>{collection.name}</Link></td>
                  <td><span className="dot" data-tone={collection.registrationEnabled ? "success" : undefined}>{collection.registrationEnabled ? t("auth.on") : t("auth.off")}</span></td>
                  <td className="num">{collection.fields.filter((field) => !field.system).length}</td>
                  <td className="mono">{collection.recordId?.kind ?? "uuid"}</td>
                  <td className="mono txt-hint">{collection.id}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </Page>
  );
}

/** /auth/collections/:name/:tab? — the collection's sign-in administration. */
export function AuthCollectionView({ name, tab }: { name: string; tab?: string }) {
  const t = useT();
  const query = useQuery(authCollectionsQuery);
  const collection = query.data?.find((candidate) => candidate.name === name);
  if (tab && !tabs.includes(tab as Tab)) return <Navigate to={`${root}/${encodeURIComponent(name)}`} replace />;
  if (query.isPending) return <Loader />;
  const crumb = <Link to={root}>{t("nav.authCollections")}</Link>;
  if (!collection) {
    return (
      <Page title={name} crumb={crumb}>
        <EmptyState icon={RiErrorWarningLine}>{query.error ? errorText(t, query.error) : t("auth.collections.missing")}</EmptyState>
      </Page>
    );
  }
  const base = `${root}/${encodeURIComponent(collection.name)}`;
  const pageTabs = tabs.map((value) => ({ to: value === "policy" ? base : `${base}/${value}`, label: t(`auth.tab.${value}`), end: value === "policy" }));
  return (
    <Page title={collection.name} crumb={crumb} tabs={pageTabs} actions={collection.id !== defaultCollectionId && <DeleteCollection collection={collection} />}>
      <TabBody key={collection.id} collection={collection} tab={(tab as Tab | undefined) ?? "policy"} />
    </Page>
  );
}

function TabBody({ collection, tab }: { collection: AuthCollection; tab: Tab }) {
  const t = useT();
  const administration = useQuery(administrationQuery(collection.id));
  const tokens = useQuery({ ...tokenPoliciesQuery(collection.id), enabled: tab === "tokens" });
  const pending = tab === "tokens" ? tokens : administration;
  if (pending.isPending) return <Loader />;
  if (pending.isError) return <ErrorState error={pending.error} busy={pending.isFetching} onRetry={() => void pending.refetch()} />;
  // Forms are keyed by the loaded data so a save or reload starts them from the stored state.
  if (tab === "tokens") return <TokensTab key={tokens.dataUpdatedAt} collection={collection} policies={tokens.data ?? []} />;
  const data = administration.data!;
  if (tab === "providers") return <ProvidersTab collection={collection} administration={data} />;
  if (tab === "mail") return <MailTab collection={collection} administration={data} />;
  return <PolicyTab key={`${administration.dataUpdatedAt}:${collection.registrationEnabled}`} collection={collection} administration={data} />;
}

/** Deleting drops the catalog collection (when the schema designer made it) and the sign-in registration. */
function DeleteCollection({ collection }: { collection: AuthCollection }) {
  const t = useT();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const remove = useMutation({
    mutationFn: async () => {
      // Registration first: the server refuses it (409) while users exist, before any data is dropped.
      await deleteAuthCollection(collection.id);
      const catalog = await queryClient.fetchQuery(collectionsQuery);
      const entry = Object.values(catalog.collections).find((candidate) => candidate.name === collection.name && candidate.type === "auth");
      if (entry) await deleteCollection(entry);
    },
    onSuccess: async () => {
      toast(t("auth.collections.deleted", { name: collection.name }));
      queryClient.removeQueries({ queryKey: administrationQuery(collection.id).queryKey });
      await queryClient.invalidateQueries({ queryKey: ["collections"] });
      await queryClient.invalidateQueries({ queryKey: ["auth"] });
      navigate(root, { replace: true });
    },
    onError: (failure) => toast(failure instanceof ApiError && failure.code === "conflict" ? t("auth.collections.hasUsers") : errorText(t, failure), "danger"),
  });
  const close = () => setOpen(false);
  return (
    <>
      <Button size="sm" variant="transparent" className="txt-danger" onClick={() => setOpen(true)}>{t("auth.collections.delete")}</Button>
      <TypedConfirmDialog open={open} title={t("auth.collections.deleteTitle", { name: collection.name })} message={t("auth.collections.deleteText")} phrase={collection.name}
        label={t("auth.collections.typeName", { name: collection.name })} confirmLabel={t("common.delete")} busy={remove.isPending} onConfirm={() => remove.mutate()} onCancel={close} />
    </>
  );
}
