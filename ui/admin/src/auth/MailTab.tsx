import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useSearchState } from "../app/search";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { RiEyeLine, RiMailSendLine } from "@remixicon/react";
import {
  getAuthAdministration,
  previewMailTemplate,
  putMailTemplate,
  putSMTPConfig,
  testSMTP,
  type AuthAdministration,
  type AuthCollection,
  type MailPreview,
  type MailTemplatePurpose,
} from "../api/authCollections";
import { Button } from "../components/Button";
import { Checkbox, Field } from "../components/Field";
import { Tabs } from "../components/Tabs";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { bareEmail, mailPurposes, placeholderFor, templateProblems, validEndpoint, type Problems, type TemplateDraft } from "./forms";
import { FormSection as Section } from "../components/FormSection";
import { sanitizeMailHtml } from "./mailPreview";
import { administrationQuery } from "./queries";
import { assertUnchanged, problemText, reportFailure, StaleError, StaleNotice, useDraft } from "./useDraft";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { SaveBar } from "../components/SaveBar";

type MailView = MailTemplatePurpose | "smtp";

/** Templates per purpose and the collection's SMTP settings; ?mail= picks the sub-view. */
export function MailTab({ collection, administration }: { collection: AuthCollection; administration: AuthAdministration }) {
  const t = useT();
  const { params, update } = useSearchState();
  const raw = params.get("mail");
  const view: MailView = raw === "smtp" || mailPurposes.includes(raw as MailTemplatePurpose) ? (raw as MailView) : "verify";
  const tabs = [...mailPurposes.map((purpose) => ({ value: purpose as MailView, label: t(`auth.mail.purpose.${purpose}`) })), { value: "smtp" as MailView, label: t("auth.mail.smtp") }];
  const template = administration.templates?.find((candidate) => candidate.purpose === view);
  const views = <Tabs label={t("auth.mail.views")} tabs={tabs} value={view} onChange={(next) => update({ mail: next })} />;
  return (
    view === "smtp" ? (
      <SmtpForm key={JSON.stringify(administration.smtp ?? null)} collection={collection} administration={administration} views={views} />
    ) : (
      <TemplateForm key={`${view}:${JSON.stringify(template ?? null)}`} collection={collection} administration={administration} purpose={view} views={views}
        initial={{ subject: template?.subject ?? "", body: template?.body ?? "", htmlBody: template?.htmlBody ?? "" }} configured={Boolean(template)} />
    )
  );
}

interface TemplateFormProps {
  collection: AuthCollection;
  administration: AuthAdministration;
  purpose: MailTemplatePurpose;
  initial: TemplateDraft;
  configured: boolean;
  views: ReactNode;
}

function TemplateForm({ collection, administration, purpose, initial, configured, views }: TemplateFormProps) {
  const t = useT();
  const queryClient = useQueryClient();
  const form = useDraft(initial);
  const { draft, patch, problems } = form;
  const [actionUrl, setActionUrl] = useState("https://example.com/auth/action?token=preview");
  const [preview, setPreview] = useState<MailPreview | null>(null);
  const error = (key: string) => problemText(t, problems, key);
  const reload = () => void queryClient.invalidateQueries({ queryKey: administrationQuery(collection.id).queryKey });
  const placeholder = placeholderFor(purpose);
  const check = () => {
    const found: Problems = templateProblems(purpose, draft);
    form.setProblems(found);
    return Object.keys(found).length === 0;
  };

  const save = useMutation({
    mutationFn: async (force: boolean) => {
      const stored = async () => (await getAuthAdministration(collection.id)).templates?.find((candidate) => candidate.purpose === purpose) ?? null;
      await assertUnchanged(force, stored, administration.templates?.find((candidate) => candidate.purpose === purpose) ?? null);
      return putMailTemplate(collection.id, purpose, { subject: draft.subject, body: draft.body, htmlBody: draft.htmlBody });
    },
    onSuccess: (saved) => {
      const others = (administration.templates ?? []).filter((candidate) => candidate.purpose !== purpose);
      queryClient.setQueryData(administrationQuery(collection.id).queryKey, { ...administration, templates: [...others, saved] });
      toast(t("auth.savedToast"));
    },
    onError: (failure) => reportFailure(t, failure),
  });
  const render = useMutation({
    mutationFn: () => previewMailTemplate(collection.id, purpose, { subject: draft.subject, body: draft.body, htmlBody: draft.htmlBody, actionUrl }),
    onSuccess: setPreview,
    onError: (failure) => reportFailure(t, failure),
  });
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    if (check()) save.mutate(false);
  };
  const onPreview = () => {
    if (!validEndpoint(actionUrl, true)) {
      form.setProblems({ actionUrl: "url" });
      return;
    }
    if (check()) render.mutate();
  };

  return (
    <div className="page-form">
      <div className="toolbar">
        <SaveBar form="auth-template" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
      </div>
      <div className="page-content">
        <div className="auth-form auth-form-wide">
          {views}
          {save.error instanceof StaleError && <StaleNotice busy={save.isPending} onReload={reload} onOverwrite={() => save.mutate(true)} />}
          <form id="auth-template" className="auth-form" onSubmit={onSubmit} noValidate>
            <p className="txt-hint">{configured ? t("auth.mail.custom") : t("auth.mail.builtIn")} {t("auth.mail.placeholders", { placeholder })}</p>
            <Field label={t("auth.mail.subject")} value={draft.subject} error={error("subject")} onChange={(event) => patch({ subject: event.target.value })} />
            <Field kind="textarea" label={t("auth.mail.text")} mono rows={6} spellCheck={false} value={draft.body} error={error("body")} onChange={(event) => patch({ body: event.target.value })} />
            <Field kind="textarea" label={t("auth.mail.html")} mono rows={8} spellCheck={false} value={draft.htmlBody} error={error("htmlBody")} hint={t("auth.mail.htmlHint")} onChange={(event) => patch({ htmlBody: event.target.value })} />
          </form>
          <Section title={t("auth.mail.preview")}>
            <div className="auth-inline">
              <Field label={t("auth.mail.actionUrl")} mono spellCheck={false} value={actionUrl} error={error("actionUrl")} onChange={(event) => setActionUrl(event.target.value)} />
              <Button variant="secondary" icon={RiEyeLine} loading={render.isPending} onClick={onPreview}>{t("auth.mail.render")}</Button>
            </div>
            {preview && (
              <div className="auth-preview" aria-live="polite">
                <p><span className="txt-hint">{t("auth.mail.subject")}</span> {preview.subject}</p>
                <div className="auth-preview-pair">
                  {preview.html && (
                    <figure>
                      <figcaption className="txt-hint">{t("auth.mail.htmlApprox")}</figcaption>
                      <HtmlPreview html={preview.html} label={t("auth.mail.html")} />
                    </figure>
                  )}
                  <figure>
                    <figcaption className="txt-hint">{t("auth.mail.text")}</figcaption>
                    <pre className="auth-code">{preview.text}</pre>
                  </figure>
                </div>
              </div>
            )}
          </Section>
        </div>
      </div>
      <UnsavedGuard dirty={form.dirty} />
    </div>
  );
}

function SmtpForm({ collection, administration, views }: { collection: AuthCollection; administration: AuthAdministration; views: ReactNode }) {
  const t = useT();
  const queryClient = useQueryClient();
  const smtp = administration.smtp;
  const form = useDraft({
    address: smtp?.address ?? "",
    from: smtp?.from ?? "",
    username: smtp?.username ?? "",
    password: "",
    serverName: smtp?.serverName ?? "",
    implicitTls: smtp?.implicitTls ?? false,
    allowInsecureLoopback: smtp?.allowInsecureLoopback ?? false,
  });
  const { draft, patch, problems } = form;
  const [recipient, setRecipient] = useState("");
  const error = (key: string) => problemText(t, problems, key);
  const reload = () => void queryClient.invalidateQueries({ queryKey: administrationQuery(collection.id).queryKey });

  const save = useMutation({
    // The whole form is sent; an empty password keeps the stored one.
    mutationFn: async (force: boolean) => {
      await assertUnchanged(force, async () => (await getAuthAdministration(collection.id)).smtp ?? null, administration.smtp ?? null);
      return putSMTPConfig(collection.id, { ...draft, password: draft.password || undefined });
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(administrationQuery(collection.id).queryKey, { ...administration, smtp: saved });
      toast(t("auth.savedToast"));
    },
    onError: (failure) => reportFailure(t, failure),
  });
  const test = useMutation({
    mutationFn: () => testSMTP(collection.id, recipient),
    onSuccess: () => toast(t("auth.mail.testSent", { recipient })),
    onError: (failure) => reportFailure(t, failure),
  });
  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const found: Problems = {};
    if (!/^[^\s:]+:\d{1,5}$/.test(draft.address)) found.address = draft.address ? "pattern" : "required";
    if (!bareEmail(draft.from)) found.from = draft.from ? "email" : "required";
    form.setProblems(found);
    if (Object.keys(found).length === 0) save.mutate(false);
  };
  const onTest = () => {
    if (!bareEmail(recipient)) {
      form.setProblems({ ...problems, recipient: "email" });
      return;
    }
    test.mutate();
  };

  return (
    <div className="page-form">
      <div className="toolbar">
        <SaveBar form="auth-smtp" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
      </div>
      <div className="page-content">
        <div className="auth-form auth-form-wide">
          {views}
          {save.error instanceof StaleError && <StaleNotice busy={save.isPending} onReload={reload} onOverwrite={() => save.mutate(true)} />}
          <form id="auth-smtp" className="auth-form" onSubmit={onSubmit} noValidate autoComplete="off">
            <p className="txt-hint">{smtp ? t("auth.mail.smtpOwn") : t("auth.mail.smtpServer")}</p>
            <div className="auth-grid">
              <Field label={t("auth.mail.address")} mono placeholder="smtp.example.com:587" value={draft.address} error={error("address")} onChange={(event) => patch({ address: event.target.value })} />
              <Field label={t("auth.mail.from")} type="email" value={draft.from} error={error("from")} onChange={(event) => patch({ from: event.target.value })} />
              <Field label={t("auth.mail.username")} autoComplete="off" value={draft.username} onChange={(event) => patch({ username: event.target.value })} />
              <Field label={t("auth.mail.password")} type="password" autoComplete="new-password" value={draft.password}
                placeholder={smtp?.passwordConfigured ? t("auth.secretKept") : undefined} hint={smtp?.passwordConfigured ? t("auth.secretStored") : t("auth.secretNone")}
                onChange={(event) => patch({ password: event.target.value })} />
              <Field label={t("auth.mail.serverName")} mono hint={t("auth.mail.serverNameHint")} value={draft.serverName} onChange={(event) => patch({ serverName: event.target.value })} />
            </div>
            <Checkbox label={t("auth.mail.implicitTls")} checked={draft.implicitTls} onChange={(event) => patch({ implicitTls: event.target.checked })} />
            <Checkbox label={t("auth.mail.loopback")} checked={draft.allowInsecureLoopback} onChange={(event) => patch({ allowInsecureLoopback: event.target.checked })} />
          </form>
          <Section title={t("auth.mail.test")} hint={t("auth.mail.testHint")}>
            <div className="auth-inline">
              <Field label={t("auth.mail.recipient")} type="email" value={recipient} error={error("recipient")} onChange={(event) => setRecipient(event.target.value)} />
              <Button variant="secondary" icon={RiMailSendLine} loading={test.isPending} disabled={form.dirty} onClick={onTest}>{t("auth.mail.sendTest")}</Button>
            </div>
          </Section>
        </div>
      </div>
      <UnsavedGuard dirty={form.dirty} />
    </div>
  );
}

/** The server's HTML, rebuilt without scripts or remote resources in a shadow root so admin styles neither leak in nor out. */
function HtmlPreview({ html, label }: { html: string; label: string }) {
  const host = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const element = host.current;
    if (!element) return;
    const root = element.shadowRoot ?? element.attachShadow({ mode: "open" });
    const page = sanitizeMailHtml(html);
    // Mail renders on a light canvas in most clients, whatever the admin theme.
    if (!page.style.getPropertyValue("background-color") && !page.style.getPropertyValue("background")) page.style.setProperty("background-color", "#ffffff");
    if (!page.style.getPropertyValue("color")) page.style.setProperty("color", "#171717");
    if (!page.style.getPropertyValue("font-family")) page.style.setProperty("font-family", "system-ui, sans-serif");
    page.style.setProperty("min-height", "100%");
    page.style.setProperty("box-sizing", "border-box");
    root.replaceChildren(page);
  }, [html]);
  return <div ref={host} className="auth-mail-frame" role="document" aria-label={label} />;
}
