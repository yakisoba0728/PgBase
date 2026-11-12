import { Navigate, useParams } from "react-router";
import { Page } from "../components/Page";
import { useT } from "../i18n";
import { MigrationView } from "./MigrationView";
import { StorageSettingsForm } from "./StorageForm";
import "../styles/storage.css";

/** /storage/:tab? — uploaded-file storage by default, then file migration and backup storage. */
export function StorageView() {
  const t = useT();
  const { tab } = useParams();
  if (tab && tab !== "migration" && tab !== "backups") return <Navigate to="/storage" replace />;
  const tabs = [
    { to: "/storage", label: t("storage.tab.files"), end: true },
    { to: "/storage/migration", label: t("storage.tab.migration") },
    { to: "/storage/backups", label: t("storage.tab.backups") },
  ];
  return (
    <Page title={t("nav.storage")} tabs={tabs}>
      {tab === "migration" ? (
        <div className="page-content">
          <MigrationView />
        </div>
      ) : (
        <StorageSettingsForm key={tab ?? "files"} kind={tab === "backups" ? "backups" : "files"} />
      )}
    </Page>
  );
}
