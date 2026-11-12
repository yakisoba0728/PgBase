import { Page } from "../components/Page";
import { useT } from "../i18n";
import { JobsActions, JobsView } from "../ops/JobsView";

export function JobsPage() {
  const t = useT();
  return (
    <Page title={t("nav.jobs")} actions={<JobsActions />}>
      <JobsView />
    </Page>
  );
}
