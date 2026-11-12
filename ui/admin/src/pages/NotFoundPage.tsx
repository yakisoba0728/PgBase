import { Link } from "react-router";
import { RiCompass3Line } from "@remixicon/react";
import { EmptyState } from "../components/Feedback";
import { Page } from "../components/Page";
import { useT } from "../i18n";

export function NotFoundPage() {
  const t = useT();
  return (
    <Page title={t("notFound.title")}>
      <EmptyState icon={RiCompass3Line} action={<Link className="link" to="/collections">{t("notFound.back")}</Link>}>{t("notFound.text")}</EmptyState>
    </Page>
  );
}
