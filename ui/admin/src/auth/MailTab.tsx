import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { RiEyeLine, RiMailSendLine } from "@remixicon/react";
import {
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
import { Section } from "./PolicyTab";
import { administrationQuery } from "./queries";
import { problemText, reportFailure, SaveBar, UnsavedGuard, useDraft } from "./useDraft";

type MailView = MailTemplatePurpose | "smtp";

/** Templates per purpose and the collection's SMTP settings; ?mail= picks the sub-view. */
export function MailTab({ collection, administration }: { collection: AuthCollection; administration: AuthAdministration }) {
  const t = useT();
  const [params, setParams] = useSearchParams();
  const raw = params.get("mail");
  const view: MailView = raw === "smtp" || mailPurposes.includes(raw as MailTemplatePurpose) ? (raw as MailView) : "verify";
  const tabs = [...mailPurposes.map((purpose) => ({ value: purpose as MailView, label: t(`auth.mail.purpose.${purpose}`) })), { value: "smtp" as MailView, label: t("auth.mail.smtp") }];
  const template = administration.templates?.find((candidate) => candidate.purpose === view);
  return (
    <div className="page-content">
      <div className="auth-form auth-form-wide">
        <Tabs label={t("auth.mail.views")} tabs={tabs} value={view} onChange={(next) => setParams((previous) => {
          const updated = new URLSearchParams(previous);
          updated.set("mail", next);
          return updated;
        })} />
        {view === "smtp" ? (
          <SmtpForm key={JSON.stringify(administration.smtp ?? null)} collection={collection} administration={administration} />
        ) : (
          <TemplateForm key={`${view}:${JSON.stringify(template ?? null)}`} collection={collection} administration={administration} purpose={view}
            initial={{ subject: template?.subject ?? "", body: template?.body ?? "", htmlBody: template?.htmlBody ?? "" }} configured={Boolean(template)} />
        )}
      </div>
    </div>
  );
}

interface TemplateFormProps {
  collection: AuthCollection;
  administration: AuthAdministration;
  purpose: MailTemplatePurpose;
  initial: TemplateDraft;
  configured: boolean;
}

function TemplateForm({ collection, administration, purpose, initial, configured }: TemplateFormProps) {
  const t = useT();
  const queryClient = useQueryClient();
  const form = useDraft(initial);
  const { draft, patch, problems } = form;
  const [actionUrl, setActionUrl] = useState("https://example.com/auth/action?token=preview");
  const [preview, setPreview] = useState<MailPreview | null>(null);
  const error = (key: string) => problemText(t, problems, key);
  const placeholder = placeholderFor(purpose);
  const check = () => {
    const found: Problems = templateProblems(purpose, draft);
    form.setProblems(found);
    return Object.keys(found).length === 0;
  };

  const save = useMutation({
    mutationFn: () => putMailTemplate(collection.id, purpose, { subject: draft.subject, body: draft.body, htmlBody: draft.htmlBody }),
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
    if (check()) save.mutate();
  };
  const onPreview = () => {
    if (!validEndpoint(actionUrl, true)) {
      form.setProblems({ actionUrl: "url" });
      return;
    }
    if (check()) render.mutate();
  };

  return (
    <>
      <form id="auth-template" className="auth-form" onSubmit={onSubmit} noValidate>
        <SaveBar form="auth-template" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
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
            <pre className="auth-code">{preview.text}</pre>
            {/* Shown as source: a srcdoc frame inherits the admin CSP, which blocks the layout's inline styles. */}
            {preview.html && <pre className="auth-code auth-code-html" aria-label={t("auth.mail.html")}>{preview.html}</pre>}
          </div>
        )}
      </Section>
      <UnsavedGuard dirty={form.dirty} />
    </>
  );
}

function SmtpForm({ collection, administration }: { collection: AuthCollection; administration: AuthAdministration }) {
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

  const save = useMutation({
    // The whole form is sent; an empty password keeps the stored one.
    mutationFn: () => putSMTPConfig(collection.id, { ...draft, password: draft.password || undefined }),
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
    if (Object.keys(found).length === 0) save.mutate();
  };
  const onTest = () => {
    if (!bareEmail(recipient)) {
      form.setProblems({ ...problems, recipient: "email" });
      return;
    }
    test.mutate();
  };

  return (
    <>
      <form id="auth-smtp" className="auth-form" onSubmit={onSubmit} noValidate autoComplete="off">
        <SaveBar form="auth-smtp" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
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
      <UnsavedGuard dirty={form.dirty} />
    </>
  );
}
