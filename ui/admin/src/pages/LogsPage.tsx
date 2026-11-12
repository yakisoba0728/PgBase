import { Page } from "../components/Page";
import { useT } from "../i18n";
import { LogsActions, LogsView } from "../ops/LogsView";

export function LogsPage() {
  const t = useT();
  return (
    <Page title={t("nav.logs")} actions={<LogsActions />}>
      <LogsView />
    </Page>
  );
}
