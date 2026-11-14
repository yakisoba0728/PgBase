import { useState, type FormEvent } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { putTokenPolicy, type AuthCollection, type TokenPolicy, type TokenPolicyPurpose } from "../api/authCollections";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { tokenProblems, type Problems, type TokenDraft } from "./forms";
import { tokenPoliciesQuery } from "./queries";
import { problemText, reportFailure, useDraft } from "./useDraft";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { SaveBar } from "../components/SaveBar";
import { Table } from "../components/Table";
import { Input } from "../components/Field";

const purposes: TokenPolicyPurpose[] = ["session", "verify", "password_reset", "email_change", "otp", "impersonation"];

/** One row per token purpose: lifetime, plus an optional key rotation (new key and version together). */
export function TokensTab({ collection, policies }: { collection: AuthCollection; policies: TokenPolicy[] }) {
  const t = useT();
  const queryClient = useQueryClient();
  // Rows stored by a save that then failed on a later row: the form stays mounted, so these replace the loaded values.
  const [saved, setSaved] = useState<TokenPolicy[]>([]);
  const byPurpose = new Map([...policies, ...saved].map((policy) => [policy.purpose, policy]));
  const rowFrom = (policy: TokenPolicy | undefined): TokenDraft => ({ ttlSeconds: policy ? String(policy.ttlSeconds) : "", key: "", keyVersion: "" });
  const initial = Object.fromEntries(purposes.map((purpose) => [purpose, rowFrom(byPurpose.get(purpose))])) as Record<TokenPolicyPurpose, TokenDraft>;
  const form = useDraft(initial);
  const { draft, problems } = form;
  const changed = purposes.filter((purpose) => JSON.stringify(draft[purpose]) !== JSON.stringify(initial[purpose]));

  const save = useMutation({
    mutationFn: async () => {
      const stored: TokenPolicyPurpose[] = [];
      for (const purpose of changed) {
        const row = draft[purpose];
        try {
          const policy = await putTokenPolicy(collection.id, purpose, { ttlSeconds: Number(row.ttlSeconds), ...(row.key ? { key: row.key, keyVersion: row.keyVersion } : {}) });
          // A stored rotation leaves the draft, so a retry never sends it twice.
          setSaved((previous) => [...previous.filter((value) => value.purpose !== purpose), policy]);
          form.patch({ [purpose]: rowFrom(policy) } as Partial<typeof initial>);
          stored.push(purpose);
        } catch (failure) {
          // The failed row and the unsent ones keep what was typed.
          form.setProblems({ [`${purpose}.ttlSeconds`]: "rowRejected" });
          if (stored.length > 0) toast(t("auth.tokens.partial", { purposes: stored.join(", ") }));
          throw failure;
        }
      }
    },
    onSuccess: () => {
      toast(t("auth.savedToast"));
      // Reloads (and remounts) only after every row saved; a failure must not drop the draft.
      void queryClient.invalidateQueries({ queryKey: tokenPoliciesQuery(collection.id).queryKey });
    },
    onError: (failure) => reportFailure(t, failure),
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
        <Input size="sm" mono className="auth-cell-input" aria-label={`${purpose} ${label}`} invalid={Boolean(error)} title={error} autoComplete="off" spellCheck={false} {...extra}
          value={draft[purpose][key]} onChange={(event) => set(purpose, { [key]: event.target.value })} />
        {error && <span className="field-error" role="alert">{error}</span>}
      </td>
    );
  };

  return (
    <div className="page-form">
      <div className="toolbar">
        <SaveBar form="auth-tokens" dirty={form.dirty} busy={save.isPending} onDiscard={form.reset} />
      </div>
      <div className="page-content">
        <form id="auth-tokens" className="auth-form auth-form-wide" onSubmit={onSubmit} noValidate><fieldset disabled={save.isPending} className="form">
          <Table className="auth-table" label={t("auth.tab.tokens")}>
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
          </Table>
          <p className="txt-hint">{t("auth.tokens.hint")}</p>
        </fieldset></form>
      </div>
      <UnsavedGuard dirty={form.dirty} />
    </div>
  );
}
