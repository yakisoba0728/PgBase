import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiCloseLine, RiFileCopyLine } from "@remixicon/react";
import { deleteAuthRecord, updateAuthRecord, type AuthCollection, type AuthRecord } from "../api/authCollections";
import {
  impersonateUser,
  revokeUserSessions,
  sendUserPasswordReset,
  sendUserVerification,
  setUserActive,
  setUserVerified,
  unlinkUserOAuthIdentity,
  type ImpersonationSession,
  type ManagedUser,
} from "../api/users";
import { ApiError } from "../api/http";
import { errorText } from "../app/queries";
import { useTimeFormat } from "../ops/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Facts } from "../components/Facts";
import { SecretView, TaskDialog, TypedConfirmDialog, copyText } from "../components/TaskDialog";
import { Loader } from "../components/Feedback";
import { Checkbox, Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT, type MessageKey } from "../i18n";
import { bareEmail, profileChanges, profileDraft, profileFields, type Problems } from "./forms";
import { ProfileFields } from "./ProfileFields";
import { authCollectionsQuery, identitiesQuery, userRecordQuery } from "./queries";
import { problemText, reportFailure, UnsavedGuard, useDraft } from "./useDraft";
import { SaveBar } from "../components/SaveBar";

type Action =
  | { kind: "disable" | "enable" | "verify" | "unverify" | "sendVerification" | "sendReset" | "revoke" | "impersonate" | "delete" }
  | { kind: "unlink"; provider: string };

const actionText: Record<Action["kind"], { title: MessageKey; text: MessageKey; confirm: MessageKey; danger?: boolean }> = {
  disable: { title: "auth.user.disableTitle", text: "auth.user.disableText", confirm: "auth.user.disable", danger: true },
  enable: { title: "auth.user.enableTitle", text: "auth.user.enableText", confirm: "auth.user.enable" },
  verify: { title: "auth.user.verifyTitle", text: "auth.user.verifyText", confirm: "auth.user.markVerified" },
  unverify: { title: "auth.user.unverifyTitle", text: "auth.user.unverifyText", confirm: "auth.user.markUnverified", danger: true },
  sendVerification: { title: "auth.user.sendVerificationTitle", text: "auth.user.sendText", confirm: "auth.user.send" },
  sendReset: { title: "auth.user.sendResetTitle", text: "auth.user.sendText", confirm: "auth.user.send" },
  revoke: { title: "auth.user.revokeTitle", text: "auth.user.revokeText", confirm: "auth.user.revoke", danger: true },
  impersonate: { title: "auth.user.impersonateTitle", text: "auth.user.impersonateText", confirm: "auth.user.impersonate", danger: true },
  unlink: { title: "auth.user.unlinkTitle", text: "auth.user.unlinkText", confirm: "auth.user.unlink", danger: true },
  delete: { title: "auth.user.deleteTitle", text: "auth.user.deleteText", confirm: "common.delete", danger: true },
};

/** One user: lifecycle actions on /api/admin/users and profile editing on the auth record. */
export function UserPane({ user, onClose }: { user: ManagedUser; onClose: () => void }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const collections = useQuery(authCollectionsQuery);
  const resolved = useQuery({ ...userRecordQuery(user, collections.data ?? []), enabled: Boolean(collections.data) });
  const identities = useQuery(identitiesQuery(user.id));
  const [action, setAction] = useState<Action | null>(null);
  const [duration, setDuration] = useState("300");
  const [session, setSession] = useState<ImpersonationSession | null>(null);
  const refreshUsers = () => queryClient.invalidateQueries({ queryKey: ["auth", "users"] });

  const run = useMutation({
    mutationFn: async (current: Action): Promise<string> => {
      switch (current.kind) {
        case "disable":
        case "enable":
          await setUserActive(user.id, current.kind === "enable");
          return t(current.kind === "enable" ? "auth.user.enabled" : "auth.user.disabledToast");
        case "verify":
        case "unverify":
          await setUserVerified(user.id, current.kind === "verify");
          return t("auth.savedToast");
        case "sendVerification":
          await sendUserVerification(user.id);
          return t("auth.user.sent", { email: user.email });
        case "sendReset":
          await sendUserPasswordReset(user.id);
          return t("auth.user.sent", { email: user.email });
        case "revoke":
          return t("auth.user.revoked", { count: await revokeUserSessions(user.id) });
        case "impersonate":
          setSession(await impersonateUser(user.id, Number(duration)));
          return t("auth.user.impersonated");
        case "unlink":
          await unlinkUserOAuthIdentity(user.id, current.provider);
          await queryClient.invalidateQueries({ queryKey: identitiesQuery(user.id).queryKey });
          return t("auth.user.unlinked", { provider: current.provider });
        case "delete":
          await deleteAuthRecord(resolved.data!.collection.id, resolved.data!.record.id);
          onClose();
          return t("auth.user.deleted", { email: user.email });
      }
    },
    onSuccess: (message, current) => {
      toast(message);
      setAction(null);
      if (current.kind !== "impersonate" && current.kind !== "unlink") void refreshUsers();
    },
    onError: (failure) => reportFailure(t, failure),
  });

  const copy = (value: string) => copyText(t, value);
  const ask = (next: Action) => {
    run.reset();
    setAction(next);
  };
  const text = action ? actionText[action.kind] : null;

  return (
    <>
      <header className="pane-header">
        <h2 className="pane-title">{user.email}</h2>
        <IconButton label={t("records.copyId")} icon={RiFileCopyLine} onClick={() => copy(user.id)} />
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
      </header>
      <div className="pane-body auth-pane">
        <Facts
          items={[
            { label: "ID", value: user.id, mono: true },
            { label: t("auth.users.collection"), value: resolved.data ? resolved.data.collection.name : resolved.isPending ? "…" : t("auth.user.unresolved"), mono: true },
            { label: t("auth.users.created"), value: time(user.createdAt, true) },
            { label: t("auth.users.updated"), value: time(user.updatedAt, true) },
          ]}
        />

        <div className="auth-actions">
          <div className="auth-action">
            <span>{t("auth.users.status")}</span>
            <span className="dot" data-tone={user.active ? "success" : undefined}>{user.active ? t("auth.users.active") : t("auth.users.disabled")}</span>
            <Button size="sm" variant="secondary" onClick={() => ask({ kind: user.active ? "disable" : "enable" })}>{user.active ? t("auth.user.disable") : t("auth.user.enable")}</Button>
          </div>
          <div className="auth-action">
            <span>{t("auth.users.verified")}</span>
            <span className="dot" data-tone={user.verified ? "success" : undefined}>{user.verified ? t("auth.users.yes") : t("auth.users.no")}</span>
            <Button size="sm" variant="secondary" onClick={() => ask({ kind: user.verified ? "unverify" : "verify" })}>{user.verified ? t("auth.user.markUnverified") : t("auth.user.markVerified")}</Button>
            {!user.verified && <Button size="sm" variant="secondary" onClick={() => ask({ kind: "sendVerification" })}>{t("auth.user.sendVerification")}</Button>}
          </div>
          <div className="auth-action">
            <span>{t("auth.user.access")}</span>
            <Button size="sm" variant="secondary" onClick={() => ask({ kind: "sendReset" })}>{t("auth.user.sendReset")}</Button>
            <Button size="sm" variant="secondary" onClick={() => ask({ kind: "revoke" })}>{t("auth.user.revoke")}</Button>
            <Button size="sm" variant="secondary" disabled={!user.active} title={user.active ? undefined : t("auth.user.impersonateDisabled")} onClick={() => ask({ kind: "impersonate" })}>{t("auth.user.impersonate")}</Button>
          </div>
        </div>

        <section className="auth-section">
          <header><h2>{t("auth.user.identities")}</h2></header>
          {identities.isPending ? (
            <Loader />
          ) : identities.isError ? (
            <p className="txt-hint">{errorText(t, identities.error)}</p>
          ) : identities.data.length === 0 ? (
            <p className="txt-hint">{t("auth.user.noIdentities")}</p>
          ) : (
            <ul className="auth-rows">
              {identities.data.map((identity) => (
                <li key={identity.provider} className="auth-row auth-row-static">
                  <span className="mono">{identity.provider}</span>
                  <span className="txt-hint auth-row-meta mono">{identity.subject}</span>
                  <Button size="sm" variant="transparent" className="txt-danger" onClick={() => ask({ kind: "unlink", provider: identity.provider })}>{t("auth.user.unlink")}</Button>
                </li>
              ))}
            </ul>
          )}
        </section>

        {resolved.isPending ? (
          <Loader />
        ) : resolved.data ? (
          <ProfileForm key={`${resolved.data.record.id}:${resolved.data.record.updatedAt}`} userId={user.id} collection={resolved.data.collection} record={resolved.data.record} onDelete={() => ask({ kind: "delete" })} />
        ) : (
          <p className="txt-hint">{resolved.isError ? errorText(t, resolved.error) : t("auth.user.unresolvedHint")}</p>
        )}
      </div>

      <ConfirmDialog open={action !== null && action.kind !== "delete"} title={text ? t(text.title, { email: user.email, provider: action?.kind === "unlink" ? action.provider : "" }) : ""} message={text ? t(text.text, { email: user.email }) : undefined}
        confirmLabel={text ? t(text.confirm) : undefined} danger={text?.danger} busy={run.isPending} onConfirm={() => action && run.mutate(action)} onCancel={() => setAction(null)}>
        {action?.kind === "impersonate" && (
          <Field kind="select" label={t("auth.user.duration")} value={duration} onChange={(event) => setDuration(event.target.value)}
            options={["60", "300", "600", "900"].map((value) => ({ value, label: t("auth.user.minutes", { count: Number(value) / 60 }) }))} />
        )}
      </ConfirmDialog>
      <TypedConfirmDialog open={action?.kind === "delete"} title={t("auth.user.deleteTitle", { email: user.email })} message={t("auth.user.deleteText")} phrase={user.email}
        label={t("auth.user.typeEmail", { email: user.email })} confirmLabel={t("common.delete")} busy={run.isPending} onConfirm={() => run.mutate({ kind: "delete" })} onCancel={() => setAction(null)} />
      {/* The token lives only in this state; closing the dialog drops it. */}
      <TaskDialog open={session !== null} title={t("auth.user.tokenTitle", { email: user.email })} onClose={() => setSession(null)}>
        {session?.token && (
          <SecretView label={t("auth.user.token")} value={session.token} warning={t("auth.user.tokenWarning")} onDone={() => setSession(null)}
            details={<p className="txt-hint">{t("auth.user.expires", { at: time(session.expiresAt, true) })}</p>} />
        )}
      </TaskDialog>
    </>
  );
}

function ProfileForm({ userId, collection, record, onDelete }: { userId: string; collection: AuthCollection; record: AuthRecord; onDelete: () => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const fields = profileFields(collection);
  const initial = { email: record.email ?? "", password: "", emailVisibility: record.emailVisibility, profile: profileDraft(fields, record.fields) };
  const form = useDraft(initial);
  const { draft, patch, problems } = form;

  const save = useMutation({
    mutationFn: (changes: Record<string, unknown>) =>
      updateAuthRecord(collection.id, record.id, {
        ...(draft.email !== initial.email ? { email: draft.email } : {}),
        ...(draft.password ? { password: draft.password } : {}),
        ...(draft.emailVisibility !== initial.emailVisibility ? { emailVisibility: draft.emailVisibility } : {}),
        ...(Object.keys(changes).length ? { fields: changes } : {}),
      }),
    onSuccess: async () => {
      toast(t("auth.savedToast"));
      await queryClient.invalidateQueries({ queryKey: ["auth", "users"] });
      await queryClient.invalidateQueries({ queryKey: ["auth", "user-record"] });
    },
    onError: (failure) => {
      if (failure instanceof ApiError && failure.code === "conflict") form.setProblems({ email: "taken" });
      else reportFailure(t, failure);
    },
  });
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const { changes, problems: found } = profileChanges(fields, draft.profile, initial.profile);
    const all: Problems = Object.fromEntries(Object.entries(found).map(([key, value]) => [`profile.${key}`, value]));
    if (!bareEmail(draft.email)) all.email = "email";
    form.setProblems(all);
    if (Object.keys(all).length === 0) save.mutate(changes);
  };
  const profileProblems = Object.fromEntries(Object.entries(problems).filter(([key]) => key.startsWith("profile.")).map(([key, value]) => [key.slice(8), value]));

  return (
    <section className="auth-section">
      <header>
        <h2>{t("auth.user.record")}</h2>
        {record.id !== userId && <p className="txt-hint mono">{record.id}</p>}
      </header>
      <form id="auth-user-record" className="form" onSubmit={onSubmit} noValidate autoComplete="off">
        <Field label={t("auth.users.email")} type="email" value={draft.email} error={problemText(t, problems, "email")} onChange={(event) => patch({ email: event.target.value })} />
        <Field label={t("auth.user.newPassword")} type="password" autoComplete="new-password" value={draft.password} hint={t("auth.user.newPasswordHint")} onChange={(event) => patch({ password: event.target.value })} />
        <Checkbox label={t("auth.user.emailVisibility")} checked={draft.emailVisibility} onChange={(event) => patch({ emailVisibility: event.target.checked })} />
        <ProfileFields t={t} fields={fields} draft={draft.profile} problems={profileProblems} onChange={(name, value) => patch({ profile: { ...draft.profile, [name]: value } })} />
      </form>
      <div className="auth-record-footer">
        <Button size="sm" variant="transparent" className="txt-danger" onClick={onDelete}>{t("auth.user.delete")}</Button>
        <SaveBar form="auth-user-record" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
      </div>
      <UnsavedGuard dirty={form.dirty} />
    </section>
  );
}
