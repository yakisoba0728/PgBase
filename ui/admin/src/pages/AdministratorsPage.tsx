import { Page } from "../components/Page";
import { useT } from "../i18n";
import { AdministratorsActions, AdministratorsView } from "../system/AdministratorsView";

export function AdministratorsPage() {
  const t = useT();
  return (
    <Page title={t("nav.administrators")} actions={<AdministratorsActions />}>
      <AdministratorsView />
    </Page>
  );
}
