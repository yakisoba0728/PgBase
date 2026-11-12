import { Navigate, useParams } from "react-router";
import { Page } from "../components/Page";
import { SchemaExport } from "../devtools/SchemaExport";
import { SchemaImport } from "../devtools/SchemaImport";
import { useT } from "../i18n";
import "../styles/schema.css";
import "../styles/devtools.css";

/** /schema-transfer (export) and /schema-transfer/import. */
export function SchemaTransferPage() {
  const t = useT();
  const { tab } = useParams();
  if (tab && tab !== "import") return <Navigate to="/schema-transfer" replace />;
  const tabs = [
    { to: "/schema-transfer", label: t("devtools.export.tab"), end: true },
    { to: "/schema-transfer/import", label: t("devtools.import.tab") },
  ];
  return (
    <Page title={t("nav.schemaTransfer")} tabs={tabs}>
      {tab === "import" ? <SchemaImport /> : <SchemaExport />}
    </Page>
  );
}
