import { Navigate, useParams } from "react-router";
import { Page } from "../components/Page";
import { useT } from "../i18n";
import { isCollectionType, NewCollectionChooser, NewCollectionEditor } from "../schema/NewCollection";

/** /new-collection/:type? — choose base, auth or view, then edit the new schema. */
export function NewCollectionPage() {
  const t = useT();
  const { type } = useParams();
  if (type === undefined) {
    return (
      <Page title={t("collections.new")}>
        <NewCollectionChooser />
      </Page>
    );
  }
  if (!isCollectionType(type)) return <Navigate to="/new-collection" replace />;
  return (
    <Page title={t("collections.new")} crumb={t(`collections.type.${type}`)}>
      <NewCollectionEditor type={type} />
    </Page>
  );
}
