import type { FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { putTokenPolicy, type AuthCollection, type TokenPolicy, type TokenPolicyPurpose } from "../api/authCollections";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { tokenProblems, type Problems, type TokenDraft } from "./forms";
import { tokenPoliciesQuery } from "./queries";
import { problemText, reportFailure, SaveBar, UnsavedGuard, useDraft } from "./useDraft";

const purposes: TokenPolicyPurpose[] = ["session", "verify", "password_reset", "email_change", "otp", "impersonation"];

/** One row per token purpose: lifetime, plus an optional key rotation (new key and version together). */
export function TokensTab({ collection, policies }: { collection: AuthCollection; policies: TokenPolicy[] }) {
  const t = useT();
  const queryClient = useQueryClient();
  const byPurpose = new Map(policies.map((policy) => [policy.purpose, policy]));
  const initial = Object.fromEntries(purposes.map((purpose) => [purpose, { ttlSeconds: byPurpose.has(purpose) ? String(byPurpose.get(purpose)!.ttlSeconds) : "", key: "", keyVersion: "" }])) as Record<TokenPolicyPurpose, TokenDraft>;
  const form = useDraft(initial);
  const { draft, problems } = form;
  const changed = purposes.filter((purpose) => JSON.stringify(draft[purpose]) !== JSON.stringify(initial[purpose]));

  const save = useMutation({
    mutationFn: async () => {
      for (const purpose of changed) {
        const row = draft[purpose];
        await putTokenPolicy(collection.id, purpose, { ttlSeconds: Number(row.ttlSeconds), ...(row.key ? { key: row.key, keyVersion: row.keyVersion } : {}) });
      }
    },
    onSuccess: () => toast(t("auth.savedToast")),
    onError: (failure) => reportFailure(t, failure),
    // Some rows may have saved before a failure, so the list is always reloaded.
    onSettled: () => queryClient.invalidateQueries({ queryKey: tokenPoliciesQuery(collection.id).queryKey }),
  });

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const found: Problems = {};
    for (const purpose of changed) {
      for (const [key, problem] of Object.entries(tokenProblems(draft[purpose], byPurpose.has(purpose)))) found[`${purpose}.${key}`] = problem;
    }
    form.setProblems(found);
    if (Object.keys(found).length === 0) save.mutate();
  };
  const set = (purpose: TokenPolicyPurpose, changes: Partial<TokenDraft>) => form.patch({ [purpose]: { ...draft[purpose], ...changes } } as Partial<typeof initial>);
  const cell = (purpose: TokenPolicyPurpose, key: keyof TokenDraft, label: string, extra: { placeholder?: string; type?: string } = {}) => {
    const error = problemText(t, problems, `${purpose}.${key}`);
    return (
      <td>
        <input className="field-control mono auth-cell-input" aria-label={`${purpose} ${label}`} aria-invalid={error ? true : undefined} title={error} autoComplete="off" spellCheck={false} {...extra}
          value={draft[purpose][key]} onChange={(event) => set(purpose, { [key]: event.target.value })} />
        {error && <span className="field-error" role="alert">{error}</span>}
      </td>
    );
  };

  return (
    <div className="page-content">
      <form id="auth-tokens" className="auth-form auth-form-wide" onSubmit={onSubmit} noValidate>
        <SaveBar form="auth-tokens" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
        <table className="grid auth-table">
          <thead>
            <tr>
              <th>{t("auth.tokens.purpose")}</th>
              <th>{t("auth.tokens.ttl")}</th>
              <th>{t("auth.tokens.version")}</th>
              <th>{t("auth.tokens.newKey")}</th>
              <th>{t("auth.tokens.newVersion")}</th>
            </tr>
          </thead>
          <tbody>
            {purposes.map((purpose) => (
              <tr key={purpose}>
                <td className="mono">{purpose}</td>
                {cell(purpose, "ttlSeconds", t("auth.tokens.ttl"), { placeholder: byPurpose.has(purpose) ? undefined : t("auth.tokens.default") })}
                <td className="mono txt-hint">{byPurpose.get(purpose)?.keyVersion ?? t("auth.tokens.default")}</td>
                {cell(purpose, "key", t("auth.tokens.newKey"), { type: "password" })}
                {cell(purpose, "keyVersion", t("auth.tokens.newVersion"))}
              </tr>
            ))}
          </tbody>
        </table>
        <p className="txt-hint">{t("auth.tokens.hint")}</p>
      </form>
      <UnsavedGuard dirty={form.dirty} />
    </div>
  );
}
