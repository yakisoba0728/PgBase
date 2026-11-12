import { Page } from "../components/Page";
import { SchemaOperations, SchemaOperationsActions } from "../devtools/SchemaOperations";
import { useT } from "../i18n";

export function SchemaOperationsPage() {
  const t = useT();
  return (
    <Page title={t("nav.schemaOperations")} actions={<SchemaOperationsActions />}>
      <SchemaOperations />
    </Page>
  );
}
