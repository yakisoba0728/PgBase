import { Page } from "../components/Page";
import { useT } from "../i18n";
import { BackupsActions, BackupsView } from "../storage/BackupsView";

export function BackupsPage() {
  const t = useT();
  return (
    <Page title={t("nav.backups")} actions={<BackupsActions />}>
      <BackupsView />
    </Page>
  );
}
