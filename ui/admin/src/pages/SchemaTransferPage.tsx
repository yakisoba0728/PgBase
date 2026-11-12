import { Navigate, useParams } from "react-router";
import { Page } from "../components/Page";
import { pageTabs, screenTabs } from "../app/navigation";
import { SchemaExport } from "../devtools/SchemaExport";
import { SchemaImport } from "../devtools/SchemaImport";
import { useT } from "../i18n";

/** /schema-transfer (export) and /schema-transfer/import. */
export function SchemaTransferPage() {
  const t = useT();
  const { tab } = useParams();
  if (tab && tab !== "import") return <Navigate to="/schema-transfer" replace />;
  const tabs = pageTabs(t, screenTabs.schemaTransfer);
  return (
    <Page title={t("nav.schemaTransfer")} tabs={tabs}>
      {tab === "import" ? <SchemaImport /> : <SchemaExport />}
    </Page>
  );
}
