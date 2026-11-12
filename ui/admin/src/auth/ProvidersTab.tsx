import { useState, type FormEvent } from "react";
import { useSearchState } from "../app/search";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiCloseLine, RiKey2Line } from "@remixicon/react";
import { deleteOAuthProvider, putOAuthProvider, type AuthAdministration, type AuthCollection, type OAuthProviderConfig } from "../api/authCollections";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Dropdown, DropdownItem } from "../components/Dropdown";
import { EmptyState } from "../components/Feedback";
import { Checkbox, Field } from "../components/Field";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { providerDraft, providerInput, providerProblems, type ProviderDraft } from "./forms";
import { administrationQuery, providerCatalogQuery } from "./queries";
import { problemText, reportFailure, UnsavedGuard, useDraft } from "./useDraft";
import { SaveBar } from "../components/SaveBar";

/** Configured providers on the left; ?provider=name edits one, ?add=preset starts a new one. */
export function ProvidersTab({ collection, administration }: { collection: AuthCollection; administration: AuthAdministration }) {
  const t = useT();
  const { params, update } = useSearchState();
  const catalog = useQuery(providerCatalogQuery);
  const providers = administration.providers ?? [];
  const openName = params.get("provider");
  const adding = params.get("add");
  const open = (changes: { provider?: string; add?: string }) => update({ provider: changes.provider, add: changes.add });
  const close = () => open({});

  const current = providers.find((provider) => provider.name === openName);
  const preset = adding === null ? undefined : catalog.data?.find((entry) => entry.name === adding) ?? { name: adding === "custom" ? "" : adding };
  const source = current ?? preset;

  const list = (
    <>
      <div className="toolbar">
        <span className="toolbar-count">{t("auth.providers.count", { count: providers.length })}</span>
        <Dropdown label={t("auth.providers.add")} icon={RiAddLine} text={t("auth.providers.add")}>
          {(catalog.data ?? []).map((entry) => (
            <DropdownItem key={entry.name} onSelect={() => open({ add: entry.name })}>{entry.name}</DropdownItem>
          ))}
          <DropdownItem onSelect={() => open({ add: "custom" })}>{t("auth.providers.custom")}</DropdownItem>
        </Dropdown>
      </div>
      {providers.length === 0 ? (
        <EmptyState icon={RiKey2Line}>{t("auth.providers.empty")}</EmptyState>
      ) : (
        <ul className="auth-rows">
          {providers.map((provider) => (
            <li key={provider.name}>
              <button type="button" className="auth-row" aria-current={provider.name === openName} onClick={() => open({ provider: provider.name })}>
                <span className="mono">{provider.name}</span>
                <span className="txt-hint auth-row-meta">{provider.clientId}</span>
                <span className="dot" data-tone={provider.enabled ? "success" : undefined}>{provider.enabled ? t("auth.on") : t("auth.off")}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </>
  );

  return (
    <SplitView
      list={list}
      paneLabel={t("auth.providers.detail")}
      onClose={close}
      pane={source ? <ProviderForm key={`${source.name}:${adding ?? ""}:${JSON.stringify(current ?? null)}`} collection={collection} administration={administration} existing={current} initial={providerDraft(source)} onClose={close} onSaved={(name) => open({ provider: name })} /> : null}
    />
  );
}

interface ProviderFormProps {
  collection: AuthCollection;
  administration: AuthAdministration;
  existing?: OAuthProviderConfig;
  initial: ProviderDraft;
  onClose: () => void;
  onSaved: (name: string) => void;
}

function ProviderForm({ collection, administration, existing, initial, onClose, onSaved }: ProviderFormProps) {
  const t = useT();
  const queryClient = useQueryClient();
  const form = useDraft<ProviderDraft>(initial);
  const { draft, patch, problems } = form;
  const [confirmRemove, setConfirmRemove] = useState(false);
  const error = (key: string) => problemText(t, problems, key);
  const key = administrationQuery(collection.id).queryKey;
  const formId = "auth-provider";

  const save = useMutation({
    mutationFn: () => putOAuthProvider(collection.id, draft.name, providerInput(draft)),
    onSuccess: (saved) => {
      const others = (administration.providers ?? []).filter((provider) => provider.name !== saved.name);
      queryClient.setQueryData(key, { ...administration, providers: [...others, saved].sort((a, b) => a.name.localeCompare(b.name)) });
      toast(t("auth.savedToast"));
      form.release.current = true;
      onSaved(saved.name);
    },
    onError: (failure) => reportFailure(t, failure),
  });
  const remove = useMutation({
    mutationFn: () => deleteOAuthProvider(collection.id, draft.name),
    onSuccess: () => {
      queryClient.setQueryData(key, { ...administration, providers: (administration.providers ?? []).filter((provider) => provider.name !== draft.name) });
      toast(t("auth.providers.removed", { name: draft.name }));
      setConfirmRemove(false);
      form.release.current = true;
      onClose();
    },
    onError: (failure) => reportFailure(t, failure),
  });

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const found = providerProblems(draft);
    if (!existing && administration.providers?.some((provider) => provider.name === draft.name)) found.name = "name";
    form.setProblems(found);
    if (Object.keys(found).length === 0) save.mutate();
  };
  const text = (name: keyof ProviderDraft, label: string, hint?: string) => (
    <Field label={label} mono spellCheck={false} autoComplete="off" value={draft[name] as string} error={error(name)} hint={hint} onChange={(event) => patch({ [name]: event.target.value })} />
  );

  return (
    <>
      <header className="pane-header">
        <h2 className="pane-title mono">{existing ? existing.name : t("auth.providers.new")}</h2>
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
      </header>
      <div className="pane-body">
        <form id={formId} className="form" onSubmit={onSubmit} noValidate>
          {!existing && text("name", t("auth.providers.name"), "a-z 0-9 _ -")}
          <Checkbox label={t("auth.providers.enabled")} checked={draft.enabled} onChange={(event) => patch({ enabled: event.target.checked })} />
          {text("clientId", t("auth.providers.clientId"))}
          <Field label={t("auth.providers.clientSecret")} type="password" mono autoComplete="new-password" value={draft.clientSecret}
            placeholder={existing?.secretConfigured ? t("auth.secretKept") : undefined} hint={existing?.secretConfigured ? t("auth.secretStored") : t("auth.secretNone")}
            onChange={(event) => patch({ clientSecret: event.target.value })} />
          {text("authorizationUrl", t("auth.providers.authorizationUrl"))}
          {text("tokenUrl", t("auth.providers.tokenUrl"))}
          {text("userInfoUrl", t("auth.providers.userInfoUrl"), t("auth.providers.userInfoHint"))}
          {text("issuer", t("auth.providers.issuer"))}
          {text("jwksUrl", t("auth.providers.jwksUrl"))}
          {text("scopes", t("auth.providers.scopes"), t("auth.providers.scopesHint"))}
          <Field kind="textarea" label={t("auth.providers.redirectUris")} mono rows={3} spellCheck={false} value={draft.redirectUris} error={error("redirectUris")} hint={t("auth.providers.redirectHint")} onChange={(event) => patch({ redirectUris: event.target.value })} />
          <div className="auth-grid">
            {text("subjectField", t("auth.providers.subjectField"), "sub")}
            {text("emailField", t("auth.providers.emailField"), "email")}
            {text("emailVerifiedField", t("auth.providers.emailVerifiedField"), "email_verified")}
            {text("avatarField", t("auth.providers.avatarField"), "picture")}
          </div>
          <Checkbox label={t("auth.providers.loopback")} checked={draft.allowInsecureLoopback} onChange={(event) => patch({ allowInsecureLoopback: event.target.checked })} />
        </form>
      </div>
      <footer className="pane-footer">
        {existing && <Button variant="transparent" className="txt-danger pane-footer-start" onClick={() => setConfirmRemove(true)}>{t("auth.providers.remove")}</Button>}
        <SaveBar form={formId} dirty={form.dirty || !existing} busy={save.isPending} onDiscard={existing ? form.reset : onClose} />
      </footer>
      <ConfirmDialog open={confirmRemove} title={t("auth.providers.removeTitle", { name: draft.name })} message={t("auth.providers.removeText")} confirmLabel={t("auth.providers.remove")} danger busy={remove.isPending}
        onConfirm={() => remove.mutate()} onCancel={() => setConfirmRemove(false)} />
      <UnsavedGuard dirty={form.dirty} release={form.release} />
    </>
  );
}
