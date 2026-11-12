import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { RiKey2Line, RiLockPasswordLine, RiMailLine, RiUploadCloud2Line } from "@remixicon/react";
import { ApiError } from "../api/http";
import { getSetupBackupJob, restoreSetupBackup, setup, uploadSetupBackup } from "../api/session";
import { errorText, setupStatusQuery } from "../app/queries";
import { adoptSession } from "../app/signout";
import { Button } from "../components/Button";
import { Field } from "../components/Field";
import { Tabs } from "../components/Tabs";
import { useT } from "../i18n";
import { FormError, formValue, GuestLayout, matchingPassword } from "./GuestLayout";

type Mode = "create" | "restore";

export function SetupScreen() {
  const t = useT();
  const [mode, setMode] = useState<Mode>("create");
  const status = useQuery(setupStatusQuery);
  if (status.data && !status.data.setupRequired) return <Navigate to="/login" replace />;
  return (
    <GuestLayout title={t("setup.title")}>
      <Tabs
        label={t("setup.title")}
        value={mode}
        onChange={setMode}
        tabs={[
          { value: "create", label: t("setup.tabCreate") },
          { value: "restore", label: t("setup.tabRestore") },
        ]}
      />
      {mode === "create" ? <CreateAdministrator /> : <RestoreBackup />}
    </GuestLayout>
  );
}

function TokenField() {
  const t = useT();
  return <Field label={t("field.setupToken")} icon={RiKey2Line} name="setupToken" required autoFocus autoComplete="off" spellCheck={false} hint={t("setup.tokenHint")} />;
}

function CreateAdministrator() {
  const t = useT();
  const navigate = useNavigate();
  const mutation = useMutation({
    mutationFn: (form: FormData) =>
      setup({ setupToken: formValue(form, "setupToken").trim(), email: formValue(form, "email"), password: matchingPassword(form) }),
    onSuccess: (session) => {
      adoptSession(session);
      navigate("/collections", { replace: true });
    },
  });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    mutation.mutate(new FormData(event.currentTarget));
  };
  return (
    <form className="form" onSubmit={onSubmit} onInput={() => mutation.isError && mutation.reset()}>
      <TokenField />
      <Field label={t("field.email")} icon={RiMailLine} name="email" type="email" required autoComplete="username" />
      <Field label={t("field.password")} icon={RiLockPasswordLine} name="password" type="password" required autoComplete="new-password" />
      <Field label={t("field.passwordConfirm")} icon={RiLockPasswordLine} name="passwordConfirm" type="password" required autoComplete="new-password" />
      <FormError error={mutation.error} credentialsKey="error.invalidSetupToken" />
      <Button type="submit" block loading={mutation.isPending}>{t("setup.submit")}</Button>
    </form>
  );
}

function RestoreBackup() {
  const t = useT();
  const start = useMutation({
    mutationFn: async (form: FormData) => {
      const setupToken = formValue(form, "setupToken").trim();
      const archive = form.get("archive") as File;
      const entry = await uploadSetupBackup(setupToken, archive);
      return { setupToken, job: await restoreSetupBackup(setupToken, entry.id) };
    },
  });
  const started = start.data;
  const job = useQuery({
    queryKey: ["setup-backup-job", started?.job.id],
    queryFn: () => getSetupBackupJob(started!.setupToken, started!.job.id),
    enabled: !!started,
    initialData: started?.job,
    // The server may be busy swapping databases, so transport and 5xx errors keep being retried.
    retry: (count, error) => !(error instanceof ApiError && error.status < 500 && error.status > 0) && count < 10,
    refetchInterval: (query) => (query.state.data?.status === "running" ? 1000 : false),
  });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    start.mutate(new FormData(event.currentTarget));
  };

  if (started && job.data) {
    // Once the restored administrators exist, the setup token stops being an authority, so the job poll answers setup_unavailable.
    const finished = job.error instanceof ApiError && job.error.code === "setup_unavailable";
    if (job.data.status === "completed" || finished) {
      return (
        <>
          <p className="notice" role="status">{t("setup.restored")}</p>
          <Link className="link" to="/login">{t("reset.toLogin")}</Link>
        </>
      );
    }
    if (job.data.status === "failed") {
      return <p className="field-error" role="alert">{t("setup.restoreFailed", { error: job.data.error ?? job.data.phase })}</p>;
    }
    return (
      <p className="notice" role="status">
        {job.error ? errorText(t, job.error) : t("setup.restoring", { phase: job.data.phase })}
      </p>
    );
  }

  return (
    <form className="form" onSubmit={onSubmit} onInput={() => start.isError && start.reset()}>
      <TokenField />
      <Field label={t("field.backupFile")} icon={RiUploadCloud2Line} name="archive" type="file" required accept=".gz,.tgz,application/gzip" />
      <FormError error={start.error} credentialsKey="error.invalidSetupToken" />
      <Button type="submit" block loading={start.isPending}>
        {start.isPending ? t("setup.uploading") : t("setup.restoreSubmit")}
      </Button>
    </form>
  );
}
