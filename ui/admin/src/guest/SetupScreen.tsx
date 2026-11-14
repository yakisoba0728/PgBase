import { useRef, useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { RiKey2Line, RiLockPasswordLine, RiMailLine, RiUploadCloud2Line } from "@remixicon/react";
import { ApiError } from "../api/http";
import { getSetupBackupJob, restoreSetupBackup, setup, uploadSetupBackup } from "../api/session";
import { errorText, setupStatusQuery } from "../app/queries";
import { formatBytes } from "../app/format";
import { adoptSession } from "../app/signout";
import { Button } from "../components/Button";
import { Field } from "../components/Field";
import { Notice } from "../components/Feedback";
import { JobProgress } from "../components/JobProgress";
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
    <GuestLayout title={t("setup.title")} lead={t("setup.lead")}>
      <Tabs
        label={t("setup.title")}
        value={mode}
        onChange={setMode}
        tabs={[
          { value: "create", label: t("setup.tabCreate") },
          { value: "restore", label: t("setup.tabRestore") },
        ]}
      />
      {/* Both stay mounted so switching tabs keeps typed values and a running restore's progress. */}
      <div hidden={mode !== "create"}><CreateAdministrator /></div>
      <div hidden={mode !== "restore"}><RestoreBackup /></div>
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
  const upload = useRef<AbortController | null>(null);
  const [uploading, setUploading] = useState<{ archive: File; fraction: number } | null>(null);
  const start = useMutation({
    mutationFn: async (form: FormData) => {
      const setupToken = formValue(form, "setupToken").trim();
      const archive = form.get("archive") as File;
      upload.current = new AbortController();
      setUploading({ archive, fraction: 0 });
      const entry = await uploadSetupBackup(setupToken, archive, upload.current.signal, (fraction) => setUploading({ archive, fraction }))
        .finally(() => setUploading(null));
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
      return <Notice tone="info" role="status" actions={<Link className="link" to="/login">{t("reset.toLogin")}</Link>}>{t("setup.restored")}</Notice>;
    }
    if (job.data.status === "failed") {
      return <Notice tone="danger">{t("setup.restoreFailed", { error: job.data.error ?? job.data.phase })}</Notice>;
    }
    return (
      <Notice tone="info" role="status">
        {job.error ? errorText(t, job.error) : t("setup.restoring", { phase: job.data.phase })}
      </Notice>
    );
  }

  return (
    <form className="form" onSubmit={onSubmit} onInput={() => start.isError && start.reset()}>
      <TokenField />
      <Field label={t("field.backupFile")} icon={RiUploadCloud2Line} name="archive" type="file" required accept=".gz,.tgz,application/gzip" />
      {/* A canceled upload is the user's own choice, not an error to show. */}
      <FormError error={upload.current?.signal.aborted ? null : start.error} credentialsKey="error.invalidSetupToken" />
      <Button type="submit" block loading={start.isPending}>
        {start.isPending ? t("setup.uploading") : t("setup.restoreSubmit")}
      </Button>
      {uploading && (
        <JobProgress
          title={t("setup.uploading")}
          state="running"
          detail={`${uploading.archive.name} · ${formatBytes(uploading.archive.size)}`}
          done={uploading.fraction}
          total={1}
          action={<Button size="sm" variant="secondary" onClick={() => upload.current?.abort()}>{t("common.cancel")}</Button>}
        />
      )}
    </form>
  );
}
