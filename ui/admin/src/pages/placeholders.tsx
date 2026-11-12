import { Link } from "react-router";
import { RiCompass3Line, type RemixiconComponentType } from "@remixicon/react";
import { EmptyState } from "../components/Feedback";
import { Page } from "../components/Page";
import { useT, type MessageKey } from "../i18n";

// Screens that arrive in later phases; these keep their navigation and routes in place.

export function PlaceholderPage({ title, icon }: { title: MessageKey; icon: RemixiconComponentType }) {
  const t = useT();
  return (
    <Page title={t(title)}>
      <EmptyState icon={icon}>{t("placeholder.empty")}</EmptyState>
    </Page>
  );
}

export function NotFoundPage() {
  const t = useT();
  return (
    <Page title={t("notFound.title")}>
      <EmptyState icon={RiCompass3Line} action={<Link className="link" to="/collections">{t("notFound.back")}</Link>}>{t("notFound.text")}</EmptyState>
    </Page>
  );
}
