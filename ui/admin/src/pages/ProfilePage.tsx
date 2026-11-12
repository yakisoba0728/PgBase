import { Page } from "../components/Page";
import { useT } from "../i18n";
import { ProfileView } from "../system/ProfileView";

export function ProfilePage() {
  const t = useT();
  return (
    <Page title={t("account.profile")}>
      <ProfileView />
    </Page>
  );
}
