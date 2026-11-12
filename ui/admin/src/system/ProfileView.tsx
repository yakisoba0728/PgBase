import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { forgetSession } from "../app/signout";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiRefreshLine } from "@remixicon/react";
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
import { allowlistAdmits, isMaskedPrefix, lines } from "./net";
import { allowlistQuery, callerAddressQuery, profileQuery } from "./queries";
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
  const navigate = useNavigate();
  const change = useMutation({
    mutationFn: (value: string) => resetAdministratorPassword(id, value),
    onSuccess: () => {
      // The server ends every session of the account, this one included.
      forgetSession();
      navigate("/login", { replace: true });
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

const hostPrefix = (address: string) => `${address}/${address.includes(":") ? 128 : 32}`;

function AllowlistForm({ prefixes, profile }: { prefixes: string[]; profile: Administrator }) {
  const t = useT();
  const queryClient = useQueryClient();
  const caller = useQuery(callerAddressQuery(profile.id));
  const [text, setText] = useState(prefixes.join("\n"));
  const [confirming, setConfirming] = useState(false);
  const entries = lines(text);
  const bad = entries.find((entry) => !isMaskedPrefix(entry));
  const address = caller.data ?? null;
  // The server does not stop a self-lockout, so a known address that the list leaves out blocks the save here.
  const locksOut = address !== null && entries.length > 0 && !allowlistAdmits(entries, address);
  const invalid = bad !== undefined ? t("system.invalid.cidr", { value: bad }) : entries.length > 64 ? t("system.invalid.tooMany", { max: 64 }) : locksOut ? t("system.profile.excludesSelf", { address }) : undefined;
  const dirty = entries.join("\n") !== prefixes.join("\n");
  const save = useMutation({
    mutationFn: () => updateIpAllowlist(entries),
    onSuccess: (value) => {
      setConfirming(false);
      queryClient.setQueryData(allowlistQuery.queryKey, value);
      toast(t("system.profile.allowlistSaved"));
      // A list that excludes this browser ends the session; the session check redirects to sign-in.
      void queryClient.invalidateQueries({ queryKey: sessionQuery.queryKey });
    },
    onError: () => setConfirming(false),
  });
  const serverError = !save.isError ? undefined : save.error instanceof ApiError && save.error.code === "invalid_profile" ? t("system.profile.allowlistInvalid") : errorText(t, save.error);
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (invalid) return;
    // Clearing the list cannot lock anyone out; anything else is confirmed.
    if (entries.length === 0) save.mutate();
    else setConfirming(true);
  };
  const confirmBody = (
    <>
      <p>{t("system.profile.allowlistConfirmText")}</p>
      <pre className="sys-code">{entries.join("\n")}</pre>
      <p className="txt-hint">{t("system.profile.recovery")}</p>
      <pre className="sys-code">{`pgbase admin ip deny --email ${profile.email} --cidr <CIDR>`}</pre>
    </>
  );
  return (
    <form className="form sys-narrow" noValidate onSubmit={onSubmit}>
      <p className="warning-note" role="note">{t("system.profile.lockout")}</p>
      <div className="sys-address">
        <span className="txt-hint">{t("system.profile.currentAddress")}</span>
        {caller.isPending ? (
          <span className="txt-hint">…</span>
        ) : address ? (
          <>
            <span className="mono">{address}</span>
            {!allowlistAdmits(entries, address) && (
              <Button size="sm" variant="secondary" onClick={() => setText([...entries, hostPrefix(address)].join("\n"))}>{t("system.profile.addAddress", { prefix: hostPrefix(address) })}</Button>
            )}
          </>
        ) : (
          <span className="txt-hint">{caller.isError ? errorText(t, caller.error) : t("system.profile.addressUnknown")}</span>
        )}
        <Button size="sm" variant="transparent" icon={RiRefreshLine} loading={caller.isFetching && !caller.isPending} onClick={() => void caller.refetch()}>{t("system.profile.detectAgain")}</Button>
      </div>
      <Field kind="textarea" label={t("system.profile.prefixes")} mono rows={4} spellCheck={false} placeholder={"203.0.113.0/24\n127.0.0.1/32"} value={text}
        error={invalid ?? serverError} hint={t("system.profile.prefixesHint")} onChange={(event) => setText(event.target.value)} />
      <div className="sys-row-end">
        <Button type="submit" disabled={!dirty || Boolean(invalid)} loading={save.isPending}>{t("records.save")}</Button>
      </div>
      {address ? (
        <ConfirmDialog open={confirming} title={t("system.profile.allowlistConfirm")} confirmLabel={t("records.save")} danger busy={save.isPending} onConfirm={() => save.mutate()} onCancel={() => setConfirming(false)}>
          {confirmBody}
        </ConfirmDialog>
      ) : (
        // Unknown address: nothing can be checked, so the administrator types their email to go ahead.
        <ConfirmDialog open={confirming} title={t("system.profile.allowlistConfirm")} message={t("system.profile.uncheckedText")} danger
          typed={{ phrase: profile.email, label: t("system.profile.typeEmail", { email: profile.email }) }} confirmLabel={t("records.save")} busy={save.isPending} onConfirm={() => save.mutate()} onCancel={() => setConfirming(false)}>
          {confirmBody}
        </ConfirmDialog>
      )}
    </form>
  );
}
