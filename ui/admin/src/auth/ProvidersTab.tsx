import { useState, type FormEvent } from "react";
import { useSearchState } from "../app/search";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiErrorWarningLine, RiKey2Line } from "@remixicon/react";
import { deleteOAuthProvider, putOAuthProvider, type AuthAdministration, type AuthCollection, type OAuthProviderConfig } from "../api/authCollections";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Notice } from "../components/Feedback";
import { Checkbox, Field, Searchbar } from "../components/Field";
import { PaneHeader, SplitView } from "../components/SplitView";
import { CopyButton } from "../components/CopyButton";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { AppleSecretButton } from "../system/AppleSecret";
import { providerDraft, providerInput, providerProblems, type ProviderDraft } from "./forms";
import { appleCallbackPath, providerPresets, type ProviderPreset } from "./presets";
import { administrationQuery, providerCatalogQuery } from "./queries";
import { isStale, problemText, reportFailure, StaleNotice, useDraft } from "./useDraft";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { SaveBar } from "../components/SaveBar";

/** Configured providers on the left; ?provider=name edits one, ?add=pick lists presets, ?add=<preset> starts one. */
export function ProvidersTab({ collection, administration }: { collection: AuthCollection; administration: AuthAdministration }) {
  const t = useT();
  const { params, update } = useSearchState();
  const catalog = useQuery(providerCatalogQuery);
  const presets = providerPresets(catalog.data);
  const providers = administration.providers ?? [];
  const openName = params.get("provider");
  const adding = params.get("add");
  const open = (changes: { provider?: string; add?: string }) => update({ provider: changes.provider, add: changes.add });
  const close = () => open({});

  const current = providers.find((provider) => provider.name === openName);
  const preset = adding === null || adding === "pick" ? undefined : presets.find((entry) => entry.name === adding) ?? { name: adding === "custom" ? "" : adding, label: "" };
  const source = current ?? preset;

  const list = (
    <>
      <div className="toolbar">
        <span className="toolbar-count">{t("auth.providers.count", { count: providers.length })}</span>
        <Button size="sm" icon={RiAddLine} data-new onClick={() => open({ add: "pick" })}>{t("auth.providers.add")}</Button>
      </div>
      {providers.length === 0 ? (
        <EmptyState icon={RiKey2Line}>{t("auth.providers.empty")}</EmptyState>
      ) : (
        <ul className="auth-rows">
          {providers.map((provider) => (
            <li key={provider.name}>
              <button type="button" className="row-btn auth-row" aria-current={provider.name === openName} onClick={() => open({ provider: provider.name })}>
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

  let pane = null;
  if (adding === "pick") pane = <PresetPicker presets={presets} taken={new Set(providers.map((provider) => provider.name))} onPick={(name) => open({ add: name })} onClose={close} />;
  else if (source) {
    pane = (
      <ProviderForm key={`${source.name}:${adding ?? ""}:${JSON.stringify(current ?? null)}`} collection={collection} administration={administration} existing={current}
        preset={presets.find((entry) => entry.name === source.name)} initial={providerDraft(source)} onClose={close} onSaved={(name) => open({ provider: name })} />
    );
  } else if (openName !== null) pane = <EmptyState icon={RiErrorWarningLine}>{t("common.notFound")}</EmptyState>;
  return <SplitView list={list} paneLabel={t("auth.providers.detail")} onClose={close} pane={pane} />;
}

function PresetPicker({ presets, taken, onPick, onClose }: { presets: ProviderPreset[]; taken: Set<string>; onPick: (name: string) => void; onClose: () => void }) {
  const t = useT();
  const [filter, setFilter] = useState("");
  const term = filter.trim().toLowerCase();
  const shown = presets.filter((preset) => !term || preset.label.toLowerCase().includes(term) || preset.name.includes(term));
  return (
    <>
      <PaneHeader title={t("auth.providers.add")} onClose={onClose} />
      <div className="pane-body auth-pane">
        <Searchbar size="sm" label={t("auth.providers.find")} autoFocus value={filter} onChange={(event) => setFilter(event.target.value)} />
        <ul className="auth-rows">
          {shown.map((preset) => (
            <li key={preset.name}>
              <button type="button" className="row-btn auth-row" disabled={taken.has(preset.name)} onClick={() => onPick(preset.name)}>
                <span>{preset.label}</span>
                <span className="txt-hint auth-row-meta mono">{preset.name}</span>
                <span className="txt-hint">{taken.has(preset.name) ? t("auth.providers.configured") : ""}</span>
              </button>
            </li>
          ))}
          <li>
            <button type="button" className="row-btn auth-row" onClick={() => onPick("custom")}>
              <span>{t("auth.providers.custom")}</span>
              <span className="txt-hint auth-row-meta">{t("auth.providers.customHint")}</span>
              <span />
            </button>
          </li>
        </ul>
      </div>
    </>
  );
}

interface ProviderFormProps {
  collection: AuthCollection;
  administration: AuthAdministration;
  existing?: OAuthProviderConfig;
  preset?: ProviderPreset;
  initial: ProviderDraft;
  onClose: () => void;
  onSaved: (name: string) => void;
}

function ProviderForm({ collection, administration, existing, preset, initial, onClose, onSaved }: ProviderFormProps) {
  const t = useT();
  const queryClient = useQueryClient();
  const form = useDraft<ProviderDraft>(initial);
  const { draft, patch, problems } = form;
  const [confirmRemove, setConfirmRemove] = useState(false);
  const error = (key: string) => problemText(t, problems, key);
  const key = administrationQuery(collection.id).queryKey;
  const formId = "auth-provider";
  const redirects = draft.redirectUris.split("\n").map((line) => line.trim()).filter(Boolean);

  const save = useMutation({
    // A new provider must not exist yet; an edit sends the loaded etag. Forcing drops the precondition.
    mutationFn: (force: boolean) => putOAuthProvider(collection.id, draft.name, providerInput(draft), force ? undefined : (existing?.etag ?? null)),
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
    if (Object.keys(found).length === 0) save.mutate(false);
  };
  const text = (name: keyof ProviderDraft, label: string, hint?: string) => (
    <Field label={label} mono spellCheck={false} autoComplete="off" value={draft[name] as string} error={error(name)} hint={hint} onChange={(event) => patch({ [name]: event.target.value })} />
  );
  const copyRow = (value: string) => (
    <li key={value} className="auth-row auth-row-static">
      <span className="mono auth-row-meta" title={value}>{value}</span>
      <span />
      <CopyButton label={t("auth.providers.copy")} value={value} />
    </li>
  );

  return (
    <>
      <PaneHeader title={existing ? existing.name : preset?.label || t("auth.providers.new")} mono={Boolean(existing)} onClose={onClose} />
      <div className="pane-body">
        {isStale(save.error) && <StaleNotice busy={save.isPending} onReload={() => void queryClient.invalidateQueries({ queryKey: key })} onOverwrite={() => save.mutate(true)} />}
        {preset?.selfHosted && !existing && <Notice role="note">{t("auth.providers.selfHosted")}</Notice>}
        {draft.name === "apple" && (
          // Outside the form: the generator is a form of its own.
          <div className="auth-apple">
            <p className="txt-hint">{t("auth.providers.appleHint")}</p>
            <AppleSecretButton clientId={draft.clientId} onGenerated={(secret) => { patch({ clientSecret: secret }); toast(t("auth.providers.appleGenerated")); }} />
          </div>
        )}
        <form id={formId} className="form" onSubmit={onSubmit} noValidate><fieldset disabled={save.isPending} className="form">
          {!existing && text("name", t("auth.providers.name"), t("auth.providers.nameHint"))}
          <Checkbox label={t("auth.providers.enabled")} checked={draft.enabled} onChange={(event) => patch({ enabled: event.target.checked })} />
          {text("clientId", draft.name === "apple" ? t("auth.providers.appleClientId") : t("auth.providers.clientId"))}
          <Field label={t("auth.providers.clientSecret")} type="password" mono autoComplete="new-password" value={draft.clientSecret}
            placeholder={existing?.secretConfigured ? t("auth.secretKept") : undefined} hint={draft.clientSecret && draft.name === "apple" ? t("auth.providers.appleFilled") : existing?.secretConfigured ? t("auth.secretStored") : t("auth.secretNone")}
            onChange={(event) => patch({ clientSecret: event.target.value })} />

          <section className="stack-section">
            <header>
              <h2>{t("auth.providers.callback")}</h2>
              <p className="txt-hint">{draft.name === "apple" ? t("auth.providers.callbackApple") : t("auth.providers.callbackHint")}</p>
            </header>
            {draft.name === "apple" && <ul className="auth-rows">{copyRow(`${window.location.origin}${appleCallbackPath}`)}</ul>}
            <Field kind="textarea" label={t("auth.providers.redirectUris")} mono rows={3} spellCheck={false} placeholder="https://app.example.com/auth/callback" value={draft.redirectUris} error={error("redirectUris")} hint={t("auth.providers.redirectHint")} onChange={(event) => patch({ redirectUris: event.target.value })} />
            {draft.name !== "apple" && redirects.length > 0 && <ul className="auth-rows">{redirects.map(copyRow)}</ul>}
          </section>

          <section className="stack-section">
            <header>
              <h2>{t("auth.providers.endpoints")}</h2>
            </header>
            {text("authorizationUrl", t("auth.providers.authorizationUrl"))}
            {text("tokenUrl", t("auth.providers.tokenUrl"))}
            {text("userInfoUrl", t("auth.providers.userInfoUrl"), t("auth.providers.userInfoHint"))}
            <div className="auth-grid">
              {text("issuer", t("auth.providers.issuer"))}
              {text("jwksUrl", t("auth.providers.jwksUrl"))}
            </div>
            {text("scopes", t("auth.providers.scopes"), t("auth.providers.scopesHint"))}
          </section>

          <section className="stack-section">
            <header>
              <h2>{t("auth.providers.mapping")}</h2>
              <p className="txt-hint">{t("auth.providers.mappingHint")}</p>
            </header>
            <div className="auth-grid">
              {text("subjectField", t("auth.providers.subjectField"), "sub")}
              {text("emailField", t("auth.providers.emailField"), "email")}
              {text("emailVerifiedField", t("auth.providers.emailVerifiedField"), "email_verified")}
              {text("avatarField", t("auth.providers.avatarField"), "picture")}
            </div>
          </section>
          <Checkbox label={t("auth.providers.loopback")} checked={draft.allowInsecureLoopback} onChange={(event) => patch({ allowInsecureLoopback: event.target.checked })} />
        </fieldset></form>
      </div>
      <footer className="pane-footer">
        {existing && <Button variant="danger" className="pane-footer-start" onClick={() => setConfirmRemove(true)}>{t("auth.providers.remove")}</Button>}
        <SaveBar form={formId} dirty={form.dirty || !existing} busy={save.isPending} onDiscard={existing ? form.reset : onClose} />
      </footer>
      <ConfirmDialog open={confirmRemove} title={t("auth.providers.removeTitle", { name: draft.name })} message={t("auth.providers.removeText")} confirmLabel={t("auth.providers.remove")} danger busy={remove.isPending}
        onConfirm={() => remove.mutate()} onCancel={() => setConfirmRemove(false)} />
      <UnsavedGuard dirty={form.dirty} release={() => form.release.current} />
    </>
  );
}
