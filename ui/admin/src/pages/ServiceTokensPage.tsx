import { Page } from "../components/Page";
import { useT } from "../i18n";
import { ServiceTokensActions, ServiceTokensView } from "../system/ServiceTokensView";

export function ServiceTokensPage() {
  const t = useT();
  return (
    <Page title={t("nav.tokens")} actions={<ServiceTokensActions />}>
      <ServiceTokensView />
    </Page>
  );
}
