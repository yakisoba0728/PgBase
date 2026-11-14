import { useRef, useState, type FormEvent, type ReactNode, type RefObject } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
import { useTimeFormat } from "../app/format";
import { Button } from "../components/Button";
import { Facts } from "../components/Facts";
import { ConfirmDialog, SecretView, TaskDialog } from "../components/Dialog";
import { QueryView } from "../components/QueryView";
import { Checkbox, Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT, type MessageKey } from "../i18n";
import { bareEmail, profileChanges, profileDraft, profileFields, type Problems } from "./forms";
import { ProfileFields } from "./ProfileFields";
import { authCollectionsQuery, identitiesQuery, refreshUserLists, userRecordQuery } from "./queries";
import { problemText, reportFailure, useDraft } from "./useDraft";
import { SaveBar } from "../components/SaveBar";
import { PaneHeader } from "../components/SplitView";
import { UnsavedGuard } from "../components/UnsavedGuard";

type Action =
  | { kind: "disable" | "enable" | "verify" | "unverify" | "sendVerification" | "sendReset" | "revoke" | "impersonate" | "delete" }
  | { kind: "unlink"; provider: string };

const actionText: Partial<Record<Action["kind"], { title: MessageKey; text: MessageKey; confirm: MessageKey; danger?: boolean }>> = {
  disable: { title: "auth.user.disableTitle", text: "auth.user.disableText", confirm: "auth.user.disable", danger: true },
  sendVerification: { title: "auth.user.sendVerificationTitle", text: "auth.user.sendText", confirm: "auth.user.send" },
  sendReset: { title: "auth.user.sendResetTitle", text: "auth.user.sendText", confirm: "auth.user.send" },
  revoke: { title: "auth.user.revokeTitle", text: "auth.user.revokeText", confirm: "auth.user.revoke", danger: true },
  impersonate: { title: "auth.user.impersonateTitle", text: "auth.user.impersonateText", confirm: "auth.user.impersonate", danger: true },
  unlink: { title: "auth.user.unlinkTitle", text: "auth.user.unlinkText", confirm: "auth.user.unlink", danger: true },
  delete: { title: "auth.user.deleteTitle", text: "auth.user.deleteText", confirm: "common.delete", danger: true },
};

// The inverse of each switch that runs without a confirmation.
const undoable: Partial<Record<Action["kind"], (id: string) => Promise<unknown>>> = {
  enable: (id) => setUserActive(id, false),
  verify: (id) => setUserVerified(id, false),
  unverify: (id) => setUserVerified(id, true),
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
  // Set right before a delete closes the pane, so unsaved profile edits do not hold a deleted user open.
  const release = useRef(false);
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
          release.current = true;
          onClose();
          return t("auth.user.deleted", { email: user.email });
      }
    },
    onSuccess: (message, current) => {
      const undo = undoable[current.kind];
      toast(message, "info", undo && { label: t("common.undo"), run: () => void undo(user.id).then(refreshUsers, (failure) => reportFailure(t, failure)) });
      setAction(null);
      // A delete changes the total; other actions keep the list where it is.
      if (current.kind === "delete") void refreshUserLists(queryClient);
      else if (current.kind !== "impersonate" && current.kind !== "unlink") void refreshUsers();
    },
    onError: (failure) => reportFailure(t, failure),
  });

  const ask = (next: Action) => {
    run.reset();
    // Trivially reversible switches run at once and offer Undo instead of asking first.
    if (undoable[next.kind]) run.mutate(next);
    else setAction(next);
  };
  const text = action ? actionText[action.kind] ?? null : null;

  const top = (
    <>
      <Facts
        items={[
          { label: "ID", value: user.id, mono: true, copy: true },
          { label: t("auth.users.collection"), value: resolved.data ? resolved.data.collection.name : resolved.isPending ? "…" : t("auth.user.unresolved"), mono: true },
          { label: t("auth.users.created"), value: time(user.createdAt, true) },
          { label: t("auth.users.updated"), value: time(user.updatedAt, true) },
        ]}
      />

      <div className="action-list">
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

      <section className="stack-section">
        <header><h2>{t("auth.user.identities")}</h2></header>
        <QueryView query={identities} skeleton="list" empty={<p className="txt-hint">{t("auth.user.noIdentities")}</p>}>
          {(data) => (
            <ul className="auth-rows">
              {data.map((identity) => (
                <li key={identity.provider} className="auth-row auth-row-static">
                  <span className="mono">{identity.provider}</span>
                  <span className="txt-hint auth-row-meta mono">{identity.subject}</span>
                  <Button size="sm" variant="transparent" className="txt-danger" onClick={() => ask({ kind: "unlink", provider: identity.provider })}>{t("auth.user.unlink")}</Button>
                </li>
              ))}
            </ul>
          )}
        </QueryView>
      </section>
    </>
  );

  return (
    <div className="pane-form">
      <PaneHeader title={user.email} copy={{ label: t("common.copyId"), value: user.id }} onClose={onClose} />
      {resolved.data ? (
        <ProfileForm key={`${resolved.data.record.id}:${resolved.data.record.updatedAt}`} userId={user.id} collection={resolved.data.collection} record={resolved.data.record} release={release} onDelete={() => ask({ kind: "delete" })}>
          {top}
        </ProfileForm>
      ) : (
        <div className="pane-body auth-pane">
          {top}
          <QueryView query={resolved} skeleton="form">{() => <p className="txt-hint">{t("auth.user.unresolvedHint")}</p>}</QueryView>
        </div>
      )}

      <ConfirmDialog open={action !== null} title={text ? t(text.title, { email: user.email, provider: action?.kind === "unlink" ? action.provider : "" }) : ""} message={text ? t(text.text, { email: user.email }) : undefined}
        confirmLabel={text ? t(text.confirm) : undefined} danger={text?.danger} busy={run.isPending}
        typed={action?.kind === "delete" || action?.kind === "revoke" ? { phrase: user.email, label: t("common.typeToConfirm", { name: user.email }) } : undefined} onConfirm={() => action && run.mutate(action)} onCancel={() => setAction(null)}>
        {action?.kind === "impersonate" && (
          <Field kind="select" label={t("auth.user.duration")} value={duration} onChange={(event) => setDuration(event.target.value)}
            options={["60", "300", "600", "900"].map((value) => ({ value, label: t("auth.user.minutes", { count: Number(value) / 60 }) }))} />
        )}
      </ConfirmDialog>
      {/* The token lives only in this state; closing the dialog drops it. */}
      <TaskDialog open={session !== null} title={t("auth.user.tokenTitle", { email: user.email })} onClose={() => setSession(null)}>
        {session?.token && (
          <SecretView label={t("auth.user.token")} value={session.token} warning={t("auth.user.tokenWarning")} onDone={() => setSession(null)}
            details={<p className="txt-hint">{t("auth.user.expires", { at: time(session.expiresAt, true) })}</p>} />
        )}
      </TaskDialog>
    </div>
  );
}

/** The pane body below `children` (facts, actions, identities) ends with the record form; Delete and Save sit in the pane footer. */
function ProfileForm({ userId, collection, record, release, onDelete, children }: { userId: string; collection: AuthCollection; record: AuthRecord; release: RefObject<boolean>; onDelete: () => void; children: ReactNode }) {
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
    <>
      <div className="pane-body auth-pane">
        {children}
        <section className="stack-section">
          <header>
            <h2>{t("auth.user.record")}</h2>
            {record.id !== userId && <p className="txt-hint mono">{record.id}</p>}
          </header>
          <form id="auth-user-record" className="form" onSubmit={onSubmit} noValidate autoComplete="off"><fieldset disabled={save.isPending} className="form">
            <Field label={t("auth.users.email")} type="email" value={draft.email} error={problemText(t, problems, "email")} onChange={(event) => patch({ email: event.target.value })} />
            <Field label={t("auth.user.newPassword")} type="password" autoComplete="new-password" value={draft.password} hint={t("auth.user.newPasswordHint")} onChange={(event) => patch({ password: event.target.value })} />
            <Checkbox label={t("auth.user.emailVisibility")} checked={draft.emailVisibility} onChange={(event) => patch({ emailVisibility: event.target.checked })} />
            <ProfileFields t={t} fields={fields} draft={draft.profile} problems={profileProblems} onChange={(name, value) => patch({ profile: { ...draft.profile, [name]: value } })} />
          </fieldset></form>
        </section>
      </div>
      <footer className="pane-footer">
        <Button variant="danger" className="pane-footer-start" onClick={onDelete}>{t("auth.user.delete")}</Button>
        <SaveBar form="auth-user-record" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
      </footer>
      <UnsavedGuard dirty={form.dirty} busy={save.isPending} release={() => release.current} />
    </>
  );
}
