import { Navigate, useParams } from "react-router";
import { Page } from "../components/Page";
import { useT } from "../i18n";
import { SettingsView } from "../system/SettingsView";
import { StatusView } from "../system/StatusView";

/** /settings (runtime settings) and /settings/status (read-only system status). */
export function SettingsPage() {
  const t = useT();
  const { tab } = useParams();
  if (tab && tab !== "status") return <Navigate to="/settings" replace />;
  const tabs = [
    { to: "/settings", label: t("system.tab.settings"), end: true },
    { to: "/settings/status", label: t("system.tab.status") },
  ];
  return (
    <Page title={t("nav.settings")} tabs={tabs}>
      {tab === "status" ? <StatusView /> : <SettingsView />}
    </Page>
  );
}
