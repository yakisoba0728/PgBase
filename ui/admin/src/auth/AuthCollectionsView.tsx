import { useState } from "react";
import { Link, Navigate, useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiErrorWarningLine, RiShieldUserLine } from "@remixicon/react";
import { deleteAuthCollection, type AuthCollection } from "../api/authCollections";
import { collectionsQuery } from "../collections/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState } from "../components/Feedback";
import { FormSection } from "../components/FormSection";
import { Page } from "../components/Page";
import { QueryView } from "../components/QueryView";
import { Row, Table } from "../components/Table";
import { PageSkeleton } from "../components/PageSkeleton";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { deleteCollection, deleteRefusal } from "../schema/apply";
import { MailTab } from "./MailTab";
import { PolicyTab } from "./PolicyTab";
import { ProvidersTab } from "./ProvidersTab";
import { administrationQuery, authCollectionsQuery, refreshUserLists, tokenPoliciesQuery } from "./queries";
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
  const actions = <Link className="btn btn-sm" data-new to="/new-collection/auth"><RiAddLine size={16} aria-hidden />{t("auth.collections.new")}</Link>;
  return (
    <Page title={t("nav.authCollections")} actions={actions}>
      <div className="page-content">
        <QueryView query={query} empty={<EmptyState icon={RiShieldUserLine}>{t("auth.collections.empty")}</EmptyState>}>
          {(collections) => (
            <Table className="auth-table" label={t("nav.authCollections")}>
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
                {collections.map((collection) => (
                  <Row key={collection.id} className="auth-link-row" onOpen={() => navigate(`${root}/${encodeURIComponent(collection.name)}`)}>
                    <td><Link className="mono" to={`${root}/${encodeURIComponent(collection.name)}`}>{collection.name}</Link></td>
                    <td><span className="dot" data-tone={collection.registrationEnabled ? "success" : undefined}>{collection.registrationEnabled ? t("auth.on") : t("auth.off")}</span></td>
                    <td className="num">{collection.fields.filter((field) => !field.system).length}</td>
                    <td className="mono">{collection.recordId?.kind ?? "uuid"}</td>
                    <td className="mono txt-hint">{collection.id}</td>
                  </Row>
                ))}
              </tbody>
            </Table>
          )}
        </QueryView>
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
  if (query.isPending) return <PageSkeleton />;
  const crumb = <Link to={root}>{t("nav.authCollections")}</Link>;
  if (!collection) {
    return (
      <Page title={name} crumb={crumb}>
        <QueryView query={query}>{() => <EmptyState icon={RiErrorWarningLine}>{t("auth.collections.missing")}</EmptyState>}</QueryView>
      </Page>
    );
  }
  const base = `${root}/${encodeURIComponent(collection.name)}`;
  const pageTabs = tabs.map((value) => ({ to: value === "policy" ? base : `${base}/${value}`, label: t(`auth.tab.${value}`), end: value === "policy" }));
  return (
    <Page title={collection.name} crumb={crumb} tabs={pageTabs}>
      <TabBody key={collection.id} collection={collection} tab={(tab as Tab | undefined) ?? "policy"} />
    </Page>
  );
}

function TabBody({ collection, tab }: { collection: AuthCollection; tab: Tab }) {
  const administration = useQuery(administrationQuery(collection.id));
  const tokens = useQuery({ ...tokenPoliciesQuery(collection.id), enabled: tab === "tokens" });
  // Forms are keyed by the loaded data so a save or reload starts them from the stored state.
  if (tab === "tokens") return <QueryView query={tokens} skeleton="form">{(policies) => <TokensTab collection={collection} policies={policies} />}</QueryView>;
  return (
    <QueryView query={administration} skeleton="form">
      {(data) =>
        tab === "providers" ? <ProvidersTab collection={collection} administration={data} />
        : tab === "mail" ? <MailTab collection={collection} administration={data} />
        : (
          <PolicyTab collection={collection} administration={data}>
            {collection.id !== defaultCollectionId && <DeleteCollection collection={collection} />}
          </PolicyTab>
        )
      }
    </QueryView>
  );
}

/** The danger zone under the policy form. A collection the schema owns is deleted through the schema, which removes its registration in the same
 * transaction; a registration without one is deleted directly. Either is refused while users remain. */
function DeleteCollection({ collection }: { collection: AuthCollection }) {
  const t = useT();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const remove = useMutation({
    mutationFn: async () => {
      const catalog = await queryClient.fetchQuery({ ...collectionsQuery, staleTime: 0 });
      const entry = Object.values(catalog.collections).find((candidate) => candidate.name === collection.name && candidate.type === "auth");
      await (entry ? deleteCollection(entry) : deleteAuthCollection(collection.id));
    },
    onSuccess: async () => {
      toast(t("auth.collections.deleted", { name: collection.name }));
      queryClient.removeQueries({ queryKey: administrationQuery(collection.id).queryKey });
      await queryClient.invalidateQueries({ queryKey: ["collections"] });
      await refreshUserLists(queryClient);
      await queryClient.invalidateQueries({ queryKey: ["auth"] });
      navigate(root, { replace: true });
    },
    onError: (failure) => toast(deleteRefusal(t, failure), "danger"),
  });
  const close = () => setOpen(false);
  return (
    <FormSection title={t("common.dangerZone")} hint={t("auth.collections.deleteText")}>
      <div>
        <Button size="sm" variant="danger" onClick={() => setOpen(true)}>{t("auth.collections.delete")}</Button>
      </div>
      <ConfirmDialog open={open} title={t("auth.collections.deleteTitle", { name: collection.name })} message={t("auth.collections.deleteText")} danger
        typed={{ phrase: collection.name, label: t("common.typeToConfirm", { name: collection.name }) }} confirmLabel={t("common.delete")} busy={remove.isPending} onConfirm={() => remove.mutate()} onCancel={close} />
    </FormSection>
  );
}
