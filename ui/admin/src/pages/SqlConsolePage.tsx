import { Page } from "../components/Page";
import { SqlConsole } from "../devtools/SqlConsole";
import { useT } from "../i18n";

export function SqlConsolePage() {
  const t = useT();
  return (
    <Page title={t("nav.sql")}>
      <SqlConsole />
    </Page>
  );
}
