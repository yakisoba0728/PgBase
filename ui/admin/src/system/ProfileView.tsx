import { useState, type FormEvent } from "react";
import { useLeaveSession } from "../app/signout";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/http";
import { updateIpAllowlist, updateProfile, type Administrator, type IpAllowlist } from "../api/session";
import { errorText, sessionQuery } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { ErrorState } from "../components/Feedback";
import { Loader } from "../components/Feedback";
import { Field } from "../components/Field";
import { FormSection } from "../components/FormSection";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { CurrentPasswordField, currentPasswordError, PasswordForm } from "./AdministratorsView";
import { allowlistCovers, isMaskedPrefix, lines } from "./net";
import { allowlistQuery, profileQuery } from "./queries";
import "../styles/system.css";

/** The signed-in administrator: email, password and the network allowlist. */
export function ProfileView() {
  const profile = useQuery(profileQuery);
  if (profile.isPending) return <Loader />;
  if (profile.isError) return <ErrorState error={profile.error} busy={profile.isFetching} onRetry={() => void profile.refetch()} />;
  return (
    <div className="sys-scroll">
      <EmailSection key={profile.data.email} profile={profile.data} />
      <PasswordSection />
      <AllowlistSection profile={profile.data} />
    </div>
  );
}

function EmailSection({ profile }: { profile: Administrator }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [email, setEmail] = useState(profile.email);
  const [current, setCurrent] = useState("");
  const save = useMutation({
    mutationFn: () => updateProfile({ currentPassword: current, email: email.trim() }),
    onSuccess: (value) => {
      setCurrent("");
      queryClient.setQueryData(profileQuery.queryKey, value);
      // The account menu reads the email from the session.
      void queryClient.invalidateQueries({ queryKey: sessionQuery.queryKey });
      toast(t("system.profile.emailSaved"));
    },
  });
  const code = save.error instanceof ApiError ? save.error.code : "";
  const currentError = currentPasswordError(t, save.error);
  const error = !save.isError || currentError ? undefined : code === "profile_conflict" ? t("system.profile.emailTaken") : code === "invalid_profile" ? t("system.profile.emailInvalid") : errorText(t, save.error);
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    save.mutate();
  };
  return (
    <FormSection title={t("system.profile.email")} hint={t("system.profile.emailHint")}>
      <form className="sys-inline" onSubmit={onSubmit}>
        <Field label={t("system.admins.email")} name="email" type="email" required autoComplete="username" value={email} error={error} onChange={(event) => setEmail(event.target.value)} />
        <Field label={t("system.profile.currentPassword")} name="currentPassword" type="password" required autoComplete="current-password" value={current} error={currentError}
          onChange={(event) => setCurrent(event.target.value)} />
        <Button type="submit" disabled={email.trim() === profile.email || !current} loading={save.isPending}>{t("records.save")}</Button>
      </form>
    </FormSection>
  );
}

function PasswordSection() {
  const t = useT();
  const leave = useLeaveSession();
  const change = useMutation({
    mutationFn: ({ value, current }: { value: string; current: string }) => updateProfile({ currentPassword: current, newPassword: value }),
    onSuccess: () => {
      // The server ends every session of the account, this one included.
      leave();
    },
  });
  const error = !change.isError ? undefined : change.error instanceof ApiError && change.error.code === "invalid_profile" ? t("system.admins.invalidPassword") : errorText(t, change.error);
  return (
    <FormSection title={t("system.profile.password")}>
      <div className="sys-narrow">
        <PasswordForm self busy={change.isPending} error={error} currentError={currentPasswordError(t, change.error)} onSubmit={(value, current) => change.mutate({ value, current })} />
      </div>
    </FormSection>
  );
}

function AllowlistSection({ profile }: { profile: Administrator }) {
  const t = useT();
  const query = useQuery(allowlistQuery);
  return (
    <FormSection title={t("system.profile.allowlist")} hint={t("system.profile.allowlistHint")}>
      {query.isPending ? (
        <Loader />
      ) : query.isError ? (
        <ErrorState error={query.error} busy={query.isFetching} onRetry={() => void query.refetch()} />
      ) : (
        <AllowlistForm key={query.data.prefixes.join(",")} allowlist={query.data} profile={profile} />
      )}
    </FormSection>
  );
}

function AllowlistForm({ allowlist, profile }: { allowlist: IpAllowlist; profile: Administrator }) {
  const { prefixes, clientAddress } = allowlist;
  const t = useT();
  const queryClient = useQueryClient();
  const [text, setText] = useState(prefixes.join("\n"));
  const [confirming, setConfirming] = useState(false);
  const [current, setCurrent] = useState("");
  const closeConfirm = () => {
    setConfirming(false);
    setCurrent("");
  };
  const entries = lines(text);
  const bad = entries.find((entry) => !isMaskedPrefix(entry));
  const invalid = bad !== undefined ? t("system.invalid.cidr", { value: bad }) : entries.length > 64 ? t("system.invalid.tooMany", { max: 64 }) : undefined;
  const dirty = entries.join("\n") !== prefixes.join("\n");
  // A convenience mirror of the server's refusal; the server decides.
  const excludesSelf = entries.length > 0 && !allowlistCovers(entries, clientAddress) ? t("system.profile.excludesSelf", { address: clientAddress }) : undefined;
  const save = useMutation({
    mutationFn: () => updateIpAllowlist(entries, current),
    onSuccess: (value) => {
      closeConfirm();
      queryClient.setQueryData(allowlistQuery.queryKey, value);
      toast(t("system.profile.allowlistSaved"));
    },
    // A wrong current password stays on the dialog's field.
    onError: (error) => currentPasswordError(t, error) === undefined && closeConfirm(),
  });
  const code = save.error instanceof ApiError ? save.error.code : "";
  const currentError = currentPasswordError(t, save.error);
  const serverError = !save.isError || currentError ? undefined
    : code === "invalid_profile" ? t("system.profile.allowlistInvalid")
    : code === "ip_allowlist_excludes_caller" ? t("system.profile.excludesSelf", { address: clientAddress })
    : errorText(t, save.error);
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (invalid || excludesSelf) return;
    // Every change is confirmed with the current password; clearing cannot lock anyone out.
    save.reset();
    setConfirming(true);
  };
  return (
    <form className="form sys-narrow" noValidate onSubmit={onSubmit}>
      <p className="warning-note" role="note">{t("system.profile.lockout")}</p>
      <p className="txt-hint">{t("system.profile.clientAddress")} <span className="mono">{clientAddress}</span></p>
      <Field kind="textarea" label={t("system.profile.prefixes")} mono rows={4} spellCheck={false} placeholder={"203.0.113.0/24\n127.0.0.1/32"} value={text}
        error={invalid ?? excludesSelf ?? serverError} hint={t("system.profile.prefixesHint")} onChange={(event) => setText(event.target.value)} />
      <div className="sys-row-end">
        <Button type="submit" disabled={!dirty || Boolean(invalid) || Boolean(excludesSelf)} loading={save.isPending}>{t("records.save")}</Button>
      </div>
      <ConfirmDialog open={confirming} title={t(entries.length === 0 ? "system.profile.allowlistClear" : "system.profile.allowlistConfirm")} confirmLabel={t("records.save")} danger={entries.length > 0} busy={save.isPending}
        confirmDisabled={!current} onConfirm={() => save.mutate()} onCancel={closeConfirm}>
        {entries.length > 0 && (
          <>
            <p>{t("system.profile.allowlistConfirmText")}</p>
            <pre className="sys-code">{entries.join("\n")}</pre>
            <p className="txt-hint">{t("system.profile.recovery")}</p>
            <pre className="sys-code">{`pgbase admin ip deny --email ${profile.email} --cidr <CIDR>`}</pre>
          </>
        )}
        <CurrentPasswordField value={current} error={currentError} onChange={setCurrent} />
      </ConfirmDialog>
    </form>
  );
}
