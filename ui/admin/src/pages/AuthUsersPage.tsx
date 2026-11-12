import { UsersActions, UsersView } from "../auth/UsersView";
import { Page } from "../components/Page";
import { useT } from "../i18n";

/** /auth/users */
export function AuthUsersPage() {
  const t = useT();
  return (
    <Page title={t("nav.users")} actions={<UsersActions />}>
      <UsersView />
    </Page>
  );
}
