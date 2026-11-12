import { Page } from "../components/Page";
import { useT } from "../i18n";
import { RealtimeActions, RealtimeView } from "../ops/RealtimeView";

export function RealtimePage() {
  const t = useT();
  return (
    <Page title={t("nav.realtime")} actions={<RealtimeActions />}>
      <RealtimeView />
    </Page>
  );
}
