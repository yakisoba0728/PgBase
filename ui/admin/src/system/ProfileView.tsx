import { useState, type FormEvent, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiErrorWarningLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { updateIpAllowlist, updateProfile, type Administrator } from "../api/session";
import { resetAdministratorPassword } from "../api/system";
import { errorText, sessionQuery } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Loader } from "../components/Feedback";
import { Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { PasswordForm } from "./AdministratorsView";
import { isMaskedPrefix, lines } from "./net";
import { allowlistQuery, profileQuery } from "./queries";
import "../styles/system.css";

/** The signed-in administrator: email, password and the network allowlist. */
export function ProfileView() {
  const t = useT();
  const profile = useQuery(profileQuery);
  if (profile.isPending) return <Loader />;
  if (profile.isError) return <EmptyState icon={RiErrorWarningLine}>{errorText(t, profile.error)}</EmptyState>;
  return (
    <div className="sys-scroll">
      <EmailSection key={profile.data.email} profile={profile.data} />
      <PasswordSection id={profile.data.id} />
      <AllowlistSection email={profile.data.email} />
    </div>
  );
}

function Section({ id, title, hint, children }: { id: string; title: string; hint: string; children: ReactNode }) {
  return (
    <section className="sys-section" aria-labelledby={`sys-${id}`}>
      <header>
        <h2 id={`sys-${id}`}>{title}</h2>
        <p className="txt-hint">{hint}</p>
      </header>
      {children}
    </section>
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
    <Section id="email" title={t("system.profile.email")} hint={t("system.profile.emailHint")}>
      <form className="sys-inline" onSubmit={onSubmit}>
        <Field label={t("system.admins.email")} name="email" type="email" required autoComplete="email" value={email} error={error} onChange={(event) => setEmail(event.target.value)} />
        <Button type="submit" disabled={email.trim() === profile.email} loading={save.isPending}>{t("records.save")}</Button>
      </form>
    </Section>
  );
}

function PasswordSection({ id }: { id: string }) {
  const t = useT();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const change = useMutation({
    mutationFn: (value: string) => resetAdministratorPassword(id, value),
    onSuccess: () => {
      // The server ends every session of the account, this one included.
      queryClient.clear();
      navigate("/login", { replace: true });
    },
  });
  const error = !change.isError ? undefined : change.error instanceof ApiError && change.error.code === "invalid_administrator" ? t("system.admins.invalidPassword") : errorText(t, change.error);
  return (
    <Section id="password" title={t("system.profile.password")} hint={t("system.profile.passwordHint")}>
      <div className="sys-narrow">
        <PasswordForm self busy={change.isPending} error={error} onSubmit={(value) => change.mutate(value)} />
      </div>
    </Section>
  );
}

function AllowlistSection({ email }: { email: string }) {
  const t = useT();
  const query = useQuery(allowlistQuery);
  return (
    <Section id="allowlist" title={t("system.profile.allowlist")} hint={t("system.profile.allowlistHint")}>
      {query.isPending ? (
        <Loader />
      ) : query.isError ? (
        <p className="field-error">{errorText(t, query.error)}</p>
      ) : (
        <AllowlistForm key={query.data.prefixes.join(",")} prefixes={query.data.prefixes} email={email} />
      )}
    </Section>
  );
}

function AllowlistForm({ prefixes, email }: { prefixes: string[]; email: string }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [text, setText] = useState(prefixes.join("\n"));
  const [confirming, setConfirming] = useState(false);
  const entries = lines(text);
  const bad = entries.find((entry) => !isMaskedPrefix(entry));
  const invalid = bad !== undefined ? t("system.invalid.cidr", { value: bad }) : entries.length > 64 ? t("system.invalid.tooMany", { max: 64 }) : undefined;
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
  return (
    <form className="form sys-narrow" noValidate onSubmit={onSubmit}>
      <p className="sys-warning" role="note">{t("system.profile.lockout")}</p>
      <Field kind="textarea" label={t("system.profile.prefixes")} mono rows={4} spellCheck={false} placeholder={"203.0.113.0/24\n127.0.0.1/32"} value={text}
        error={invalid ?? serverError} hint={t("system.profile.prefixesHint")} onChange={(event) => setText(event.target.value)} />
      <div className="sys-row-end">
        <Button type="submit" disabled={!dirty} loading={save.isPending}>{t("records.save")}</Button>
      </div>
      <ConfirmDialog open={confirming} title={t("system.profile.allowlistConfirm")} confirmLabel={t("records.save")} danger busy={save.isPending} onConfirm={() => save.mutate()} onCancel={() => setConfirming(false)}>
        <p>{t("system.profile.allowlistConfirmText")}</p>
        <pre className="sys-code">{entries.join("\n")}</pre>
        <p className="txt-hint">{t("system.profile.recovery")}</p>
        <pre className="sys-code">{`pgbase admin ip deny --email ${email} --cidr <CIDR>`}</pre>
      </ConfirmDialog>
    </form>
  );
}
