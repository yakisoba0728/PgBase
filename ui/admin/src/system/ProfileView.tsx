import { useState, type FormEvent } from "react";
import { useLeaveSession } from "../app/signout";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api/http";
import { updateIpAllowlist, updateProfile, type Administrator } from "../api/session";
import { resetAdministratorPassword } from "../api/system";
import { errorText, sessionQuery } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { ErrorState } from "../components/Feedback";
import { Loader } from "../components/Feedback";
import { Field } from "../components/Field";
import { FormSection } from "../components/FormSection";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { PasswordForm } from "./AdministratorsView";
import { allowlistCovers, isAddrOrPrefix, isMaskedPrefix, lines } from "./net";
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
      <PasswordSection id={profile.data.id} />
      <AllowlistSection profile={profile.data} />
    </div>
  );
}

function EmailSection({ profile }: { profile: Administrator }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [email, setEmail] = useState(profile.email);
  const save = useMutation({
    mutationFn: () => updateProfile(email.trim()),
    onSuccess: (value) => {
      queryClient.setQueryData(profileQuery.queryKey, value);
      // The account menu reads the email from the session.
      void queryClient.invalidateQueries({ queryKey: sessionQuery.queryKey });
      toast(t("system.profile.emailSaved"));
    },
  });
  const code = save.error instanceof ApiError ? save.error.code : "";
  const error = !save.isError ? undefined : code === "profile_conflict" ? t("system.profile.emailTaken") : code === "invalid_profile" ? t("system.profile.emailInvalid") : errorText(t, save.error);
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    save.mutate();
  };
  return (
    <FormSection title={t("system.profile.email")} hint={t("system.profile.emailHint")}>
      <form className="sys-inline" onSubmit={onSubmit}>
        <Field label={t("system.admins.email")} name="email" type="email" required autoComplete="email" value={email} error={error} onChange={(event) => setEmail(event.target.value)} />
        <Button type="submit" disabled={email.trim() === profile.email} loading={save.isPending}>{t("records.save")}</Button>
      </form>
    </FormSection>
  );
}

function PasswordSection({ id }: { id: string }) {
  const t = useT();
  const leave = useLeaveSession();
  const change = useMutation({
    mutationFn: (value: string) => resetAdministratorPassword(id, value),
    onSuccess: () => {
      // The server ends every session of the account, this one included.
      leave();
    },
  });
  const error = !change.isError ? undefined : change.error instanceof ApiError && change.error.code === "invalid_administrator" ? t("system.admins.invalidPassword") : errorText(t, change.error);
  return (
    <FormSection title={t("system.profile.password")}>
      <div className="sys-narrow">
        <PasswordForm self busy={change.isPending} error={error} onSubmit={(value) => change.mutate(value)} />
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
        <AllowlistForm key={query.data.prefixes.join(",")} prefixes={query.data.prefixes} profile={profile} />
      )}
    </FormSection>
  );
}

function AllowlistForm({ prefixes, profile }: { prefixes: string[]; profile: Administrator }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [text, setText] = useState(prefixes.join("\n"));
  const [confirming, setConfirming] = useState(false);
  // No endpoint reports the address the server sees, so the administrator states it; it is only compared here.
  const [own, setOwn] = useState("");
  const entries = lines(text);
  const bad = entries.find((entry) => !isMaskedPrefix(entry));
  const invalid = bad !== undefined ? t("system.invalid.cidr", { value: bad }) : entries.length > 64 ? t("system.invalid.tooMany", { max: 64 }) : undefined;
  const dirty = entries.join("\n") !== prefixes.join("\n");
  const covered = allowlistCovers(entries, own.trim());
  const ownError = !own.trim() || covered ? undefined : isAddrOrPrefix(own.trim()) ? t("system.profile.excludesSelf", { address: own.trim() }) : t("system.invalid.ip", { value: own.trim() });
  const close = () => {
    setConfirming(false);
    setOwn("");
  };
  const save = useMutation({
    mutationFn: () => updateIpAllowlist(entries),
    onSuccess: (value) => {
      close();
      queryClient.setQueryData(allowlistQuery.queryKey, value);
      toast(t("system.profile.allowlistSaved"));
      // A list that excludes this browser ends the session; the session check redirects to sign-in.
      void queryClient.invalidateQueries({ queryKey: sessionQuery.queryKey });
    },
    onError: close,
  });
  const serverError = !save.isError ? undefined : save.error instanceof ApiError && save.error.code === "invalid_profile" ? t("system.profile.allowlistInvalid") : errorText(t, save.error);
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (invalid) return;
    // Clearing the list cannot lock anyone out; anything else is confirmed against the stated address.
    if (entries.length === 0) save.mutate();
    else setConfirming(true);
  };
  return (
    <form className="form sys-narrow" noValidate onSubmit={onSubmit}>
      <p className="warning-note" role="note">{t("system.profile.lockout")}</p>
      <Field kind="textarea" label={t("system.profile.prefixes")} mono rows={4} spellCheck={false} placeholder={"203.0.113.0/24\n127.0.0.1/32"} value={text}
        error={invalid ?? serverError} hint={t("system.profile.prefixesHint")} onChange={(event) => setText(event.target.value)} />
      <div className="sys-row-end">
        <Button type="submit" disabled={!dirty || Boolean(invalid)} loading={save.isPending}>{t("records.save")}</Button>
      </div>
      <ConfirmDialog open={confirming} title={t("system.profile.allowlistConfirm")} confirmLabel={t("records.save")} danger busy={save.isPending} confirmDisabled={!covered}
        onConfirm={() => save.mutate()} onCancel={close}>
        <p>{t("system.profile.allowlistConfirmText")}</p>
        <pre className="sys-code">{entries.join("\n")}</pre>
        <Field label={t("system.profile.ownAddress")} mono autoComplete="off" spellCheck={false} placeholder="203.0.113.9" value={own} error={ownError} hint={t("system.profile.ownAddressHint")}
          onChange={(event) => setOwn(event.target.value)} />
        <p className="txt-hint">{t("system.profile.recovery")}</p>
        <pre className="sys-code">{`pgbase admin ip deny --email ${profile.email} --cidr <CIDR>`}</pre>
      </ConfirmDialog>
    </form>
  );
}
