import type { FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { setAuthPolicy, updateAuthCollection, type AuthAdministration, type AuthCollection } from "../api/authCollections";
import { Checkbox, Field } from "../components/Field";
import { FormSection } from "../components/FormSection";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { policyDraft, policyInput, policyProblems, type PolicyDraft } from "./forms";
import { administrationQuery } from "./queries";
import { isStale, problemText, reportFailure, StaleNotice, useDraft } from "./useDraft";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { SaveBar } from "../components/SaveBar";

/** Registration (on the collection) plus every field of PUT /policy, saved together. */
export function PolicyTab({ collection, administration }: { collection: AuthCollection; administration: AuthAdministration }) {
  const t = useT();
  const queryClient = useQueryClient();
  const initial = policyDraft(administration.policy, collection.registrationEnabled);
  const form = useDraft<PolicyDraft>(initial);
  const { draft, patch, problems } = form;
  const error = (key: string) => problemText(t, problems, key);

  const save = useMutation({
    // The policy goes first: a stale one (412) is refused before registration changes. Forcing drops If-Match.
    mutationFn: async (force: boolean) => {
      const policy = await setAuthPolicy(collection.id, policyInput(draft), force ? undefined : administration.policy.etag);
      // Without fields or rules, PATCH keeps the stored ones, so the toggle cannot drop a field added meanwhile.
      if (draft.registrationEnabled !== collection.registrationEnabled) await updateAuthCollection(collection.id, { registrationEnabled: draft.registrationEnabled });
      return policy;
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
    if (Object.keys(found).length === 0) save.mutate(false);
  };
  const number = (key: keyof PolicyDraft, label: string, hint?: string) => (
    <Field label={label} mono inputMode="numeric" value={draft[key] as string} error={error(key)} hint={hint} onChange={(event) => patch({ [key]: event.target.value })} />
  );

  return (
    <div className="page-form">
      <div className="toolbar">
        <SaveBar form="auth-policy" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
      </div>
      <div className="page-content">
        {isStale(save.error) && (
          <StaleNotice busy={save.isPending} onReload={() => void queryClient.invalidateQueries({ queryKey: administrationQuery(collection.id).queryKey })} onOverwrite={() => save.mutate(true)} />
        )}
        <form id="auth-policy" className="auth-form auth-form-wide" onSubmit={onSubmit} noValidate>
          <FormSection title={t("auth.policy.registration")} hint={t("auth.policy.registrationHint")}>
            <Checkbox label={t("auth.policy.registrationEnabled")} checked={draft.registrationEnabled} onChange={(event) => patch({ registrationEnabled: event.target.checked })} />
          </FormSection>
          <FormSection title={t("auth.policy.password")}>
            <Checkbox label={t("auth.policy.passwordEnabled")} checked={draft.passwordEnabled} onChange={(event) => patch({ passwordEnabled: event.target.checked })} />
            <div className="auth-grid">
              {number("minimumPasswordBytes", t("auth.policy.minBytes"), "12–128")}
              {number("maximumPasswordBytes", t("auth.policy.maxBytes"), "≤ 4096")}
              <Field label={t("auth.policy.pattern")} mono spellCheck={false} value={draft.passwordPattern} error={error("passwordPattern")} hint={t("auth.policy.patternHint")} onChange={(event) => patch({ passwordPattern: event.target.value })} />
            </div>
          </FormSection>
          <FormSection title={t("auth.policy.identity")} hint={t("auth.policy.identityHint")}>
            <Field label={t("auth.policy.identityFields")} mono spellCheck={false} value={draft.identityFields} error={error("identityFields")} onChange={(event) => patch({ identityFields: event.target.value })} />
          </FormSection>
          <FormSection title={t("auth.policy.mfa")} hint={t("auth.policy.mfaHint")}>
            <Checkbox label={t("auth.policy.mfaEnabled")} checked={draft.mfaEnabled} onChange={(event) => patch({ mfaEnabled: event.target.checked })} />
            <div className="auth-grid">
              {number("mfaDurationSeconds", t("auth.policy.mfaDuration"), "60–86400")}
              <Field label={t("auth.policy.mfaRule")} mono spellCheck={false} placeholder='{"field":"verified","equals":true}' value={draft.mfaRule} error={error("mfaRule")} hint={t("auth.policy.mfaRuleHint")} onChange={(event) => patch({ mfaRule: event.target.value })} />
            </div>
          </FormSection>
          <FormSection title={t("auth.policy.otp")} hint={t("auth.policy.otpHint")}>
            <Checkbox label={t("auth.policy.otpEnabled")} checked={draft.otpEnabled} onChange={(event) => patch({ otpEnabled: event.target.checked })} />
            <div className="auth-grid">
              {number("otpDurationSeconds", t("auth.policy.otpDuration"), "60–900")}
              {number("otpMaxAttempts", t("auth.policy.otpAttempts"), "1–10")}
            </div>
          </FormSection>
          <p className="txt-hint">{t("auth.policy.sessionHint")}</p>
        </form>
      </div>
      <UnsavedGuard dirty={form.dirty} />
    </div>
  );
}
