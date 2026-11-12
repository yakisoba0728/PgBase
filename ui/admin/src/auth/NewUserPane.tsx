import type { FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiCloseLine } from "@remixicon/react";
import { createAuthRecord, type AuthCollection } from "../api/authCollections";
import { ApiError } from "../api/http";
import { IconButton } from "../components/Button";
import { Loader } from "../components/Feedback";
import { Checkbox, Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { bareEmail, profileChanges, profileDraft, profileFields, type Problems, type ProfileDraft } from "./forms";
import { ProfileFields } from "./ProfileFields";
import { administrationQuery, authCollectionsQuery } from "./queries";
import { problemText, reportFailure, UnsavedGuard, useDraft } from "./useDraft";
import { SaveBar } from "../components/SaveBar";

/** Creates a user with credentials through the auth-collection records endpoint. */
export function NewUserPane({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const t = useT();
  const collections = useQuery(authCollectionsQuery);
  return (
    <>
      <header className="pane-header">
        <h2 className="pane-title">{t("auth.users.new")}</h2>
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
      </header>
      {collections.data ? <NewUserForm collections={collections.data} onClose={onClose} onCreated={onCreated} /> : <Loader />}
    </>
  );
}

interface Draft {
  collection: string;
  email: string;
  password: string;
  verified: boolean;
  emailVisibility: boolean;
  profile: ProfileDraft;
}

function NewUserForm({ collections, onClose, onCreated }: { collections: AuthCollection[]; onClose: () => void; onCreated: (id: string) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const first = collections[0];
  const form = useDraft<Draft>({ collection: first?.id ?? "", email: "", password: "", verified: false, emailVisibility: false, profile: first ? profileDraft(profileFields(first), null) : {} });
  const { draft, patch, problems } = form;
  const collection = collections.find((candidate) => candidate.id === draft.collection);
  const fields = collection ? profileFields(collection) : [];
  const policy = useQuery({ ...administrationQuery(draft.collection), enabled: Boolean(collection) });
  const minimum = policy.data?.policy.minimumPasswordBytes ?? 12;

  const create = useMutation({
    mutationFn: (changes: Record<string, unknown>) =>
      createAuthRecord(draft.collection, { email: draft.email, password: draft.password, verified: draft.verified, emailVisibility: draft.emailVisibility, ...(Object.keys(changes).length ? { fields: changes } : {}) }),
    onSuccess: async (record) => {
      toast(t("auth.users.created_toast", { email: record.email ?? draft.email }));
      form.release.current = true;
      await queryClient.invalidateQueries({ queryKey: ["auth", "users"] });
      // Under the uuid id policy the record id is the user id; text ids cannot be mapped back here.
      if (collection?.recordId?.kind === "text") onClose();
      else onCreated(record.id);
    },
    onError: (failure) => {
      if (failure instanceof ApiError && failure.code === "conflict") form.setProblems({ email: "taken" });
      else reportFailure(t, failure);
    },
  });

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const { changes, problems: found } = profileChanges(fields, draft.profile, profileDraft(fields, null));
    const all: Problems = Object.fromEntries(Object.entries(found).map(([key, value]) => [`profile.${key}`, value]));
    if (!collection) all.collection = "required";
    if (!bareEmail(draft.email)) all.email = draft.email ? "email" : "required";
    if (new TextEncoder().encode(draft.password).length < minimum) all.password = "short";
    form.setProblems(all);
    if (Object.keys(all).length === 0) create.mutate(changes);
  };
  const profileProblems = Object.fromEntries(Object.entries(problems).filter(([key]) => key.startsWith("profile.")).map(([key, value]) => [key.slice(8), value]));

  return (
    <>
      <div className="pane-body">
        <form id="auth-new-user" className="form" onSubmit={onSubmit} noValidate autoComplete="off">
          <Field kind="select" label={t("auth.users.collection")} value={draft.collection} error={problemText(t, problems, "collection")}
            options={collections.map((candidate) => ({ value: candidate.id, label: candidate.name }))}
            onChange={(event) => {
              const next = collections.find((candidate) => candidate.id === event.target.value);
              patch({ collection: event.target.value, profile: next ? profileDraft(profileFields(next), null) : {} });
            }} />
          <Field label={t("auth.users.email")} type="email" required value={draft.email} error={problemText(t, problems, "email")} onChange={(event) => patch({ email: event.target.value })} />
          <Field label={t("field.password")} type="password" required autoComplete="new-password" value={draft.password} error={problemText(t, problems, "password")}
            hint={t("auth.users.passwordHint", { min: minimum })} onChange={(event) => patch({ password: event.target.value })} />
          <Checkbox label={t("auth.users.verified")} checked={draft.verified} onChange={(event) => patch({ verified: event.target.checked })} />
          <Checkbox label={t("auth.user.emailVisibility")} checked={draft.emailVisibility} onChange={(event) => patch({ emailVisibility: event.target.checked })} />
          <ProfileFields t={t} fields={fields} draft={draft.profile} problems={profileProblems} onChange={(name, value) => patch({ profile: { ...draft.profile, [name]: value } })} />
        </form>
      </div>
      <footer className="pane-footer">
        <SaveBar form="auth-new-user" dirty busy={create.isPending} onDiscard={onClose} saveLabel={t("auth.users.create")} />
      </footer>
      <UnsavedGuard dirty={form.dirty} release={form.release} />
    </>
  );
}
