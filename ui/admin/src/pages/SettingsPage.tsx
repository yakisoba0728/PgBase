import { Navigate, useParams } from "react-router";
import { Page } from "../components/Page";
import { pageTabs, screenTabs } from "../app/navigation";
import { useT } from "../i18n";
import { SettingsView } from "../system/SettingsView";
import { StatusView } from "../system/StatusView";

/** /settings (runtime settings) and /settings/status (read-only system status). */
export function SettingsPage() {
  const t = useT();
  const { tab } = useParams();
  if (tab && tab !== "status") return <Navigate to="/settings" replace />;
  const tabs = pageTabs(t, screenTabs.settings);
  return (
    <Page title={t("nav.settings")} tabs={tabs}>
      {tab === "status" ? <StatusView /> : <SettingsView />}
    </Page>
  );
}
