import type { FormEvent, ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { setAuthPolicy, updateAuthCollection, type AuthAdministration, type AuthCollection } from "../api/authCollections";
import { Checkbox, Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { policyDraft, policyInput, policyProblems, type PolicyDraft } from "./forms";
import { administrationQuery } from "./queries";
import { problemText, reportFailure, SaveBar, UnsavedGuard, useDraft } from "./useDraft";

export function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="auth-section">
      <header>
        <h2>{title}</h2>
        {hint && <p className="txt-hint">{hint}</p>}
      </header>
      {children}
    </section>
  );
}

/** Registration (on the collection) plus every field of PUT /policy, saved together. */
export function PolicyTab({ collection, administration }: { collection: AuthCollection; administration: AuthAdministration }) {
  const t = useT();
  const queryClient = useQueryClient();
  const initial = policyDraft(administration.policy, collection.registrationEnabled);
  const form = useDraft<PolicyDraft>(initial);
  const { draft, patch, problems } = form;
  const error = (key: string) => problemText(t, problems, key);

  const save = useMutation({
    mutationFn: async () => {
      if (draft.registrationEnabled !== collection.registrationEnabled) {
        // PATCH needs the field list; resending the collection's own fields leaves the schema as is.
        const fields = collection.fields.filter((field) => !field.system);
        await updateAuthCollection(collection.id, { registrationEnabled: draft.registrationEnabled, fields });
      }
      return setAuthPolicy(collection.id, policyInput(draft));
    },
    onSuccess: async (policy) => {
      queryClient.setQueryData(administrationQuery(collection.id).queryKey, { ...administration, policy });
      await queryClient.invalidateQueries({ queryKey: ["auth", "collections"] });
      toast(t("auth.savedToast"));
    },
    onError: (failure) => reportFailure(t, failure),
  });

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const found = policyProblems(draft);
    form.setProblems(found);
    if (Object.keys(found).length === 0) save.mutate();
  };
  const number = (key: keyof PolicyDraft, label: string, hint?: string) => (
    <Field label={label} mono inputMode="numeric" value={draft[key] as string} error={error(key)} hint={hint} onChange={(event) => patch({ [key]: event.target.value })} />
  );

  return (
    <div className="page-content">
      <form id="auth-policy" className="auth-form" onSubmit={onSubmit} noValidate>
        <SaveBar form="auth-policy" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
        <Section title={t("auth.policy.registration")} hint={t("auth.policy.registrationHint")}>
          <Checkbox label={t("auth.policy.registrationEnabled")} checked={draft.registrationEnabled} onChange={(event) => patch({ registrationEnabled: event.target.checked })} />
        </Section>
        <Section title={t("auth.policy.password")}>
          <Checkbox label={t("auth.policy.passwordEnabled")} checked={draft.passwordEnabled} onChange={(event) => patch({ passwordEnabled: event.target.checked })} />
          <div className="auth-grid">
            {number("minimumPasswordBytes", t("auth.policy.minBytes"), "12–128")}
            {number("maximumPasswordBytes", t("auth.policy.maxBytes"), "≤ 4096")}
            <Field label={t("auth.policy.pattern")} mono spellCheck={false} value={draft.passwordPattern} error={error("passwordPattern")} hint={t("auth.policy.patternHint")} onChange={(event) => patch({ passwordPattern: event.target.value })} />
          </div>
        </Section>
        <Section title={t("auth.policy.identity")} hint={t("auth.policy.identityHint")}>
          <Field label={t("auth.policy.identityFields")} mono spellCheck={false} value={draft.identityFields} error={error("identityFields")} onChange={(event) => patch({ identityFields: event.target.value })} />
        </Section>
        <Section title={t("auth.policy.mfa")} hint={t("auth.policy.mfaHint")}>
          <Checkbox label={t("auth.policy.mfaEnabled")} checked={draft.mfaEnabled} onChange={(event) => patch({ mfaEnabled: event.target.checked })} />
          <div className="auth-grid">
            {number("mfaDurationSeconds", t("auth.policy.mfaDuration"), "60–86400")}
            <Field label={t("auth.policy.mfaRule")} mono spellCheck={false} placeholder='{"field":"verified","equals":true}' value={draft.mfaRule} error={error("mfaRule")} hint={t("auth.policy.mfaRuleHint")} onChange={(event) => patch({ mfaRule: event.target.value })} />
          </div>
        </Section>
        <Section title={t("auth.policy.otp")} hint={t("auth.policy.otpHint")}>
          <Checkbox label={t("auth.policy.otpEnabled")} checked={draft.otpEnabled} onChange={(event) => patch({ otpEnabled: event.target.checked })} />
          <div className="auth-grid">
            {number("otpDurationSeconds", t("auth.policy.otpDuration"), "60–900")}
            {number("otpMaxAttempts", t("auth.policy.otpAttempts"), "1–10")}
          </div>
        </Section>
        <p className="txt-hint">{t("auth.policy.sessionHint")}</p>
      </form>
      <UnsavedGuard dirty={form.dirty} />
    </div>
  );
}
