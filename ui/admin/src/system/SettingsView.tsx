import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiDeleteBinLine, RiRefreshLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { reloadSettings, updateSettings, type RuntimeSettings } from "../api/system";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { SaveBar } from "../components/SaveBar";
import { Loader, Notice } from "../components/Feedback";
import { ErrorState } from "../components/Feedback";
import { FormSection } from "../components/FormSection";
import { Checkbox, Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT, type MessageKey, type Translate } from "../i18n";
import { isDirty, sections, toDraft, toPatch, validate, type FieldError, type FieldSpec, type RuleDraft, type SettingsDraft } from "./settingsForm";
import { settingsQuery } from "./queries";
import "../styles/system.css";

const audiences = ["", "@guest", "@auth"];
const emptyRule: RuleDraft = { label: "", audience: "", duration: "60", maxRequests: "100" };
const label = (t: Translate, path: string) => t(`system.field.${path}` as MessageKey);
const hints = new Set(["batch.maxBodyBytes", "backups.schedule", "backups.timezone", "backups.keep", "proxy.trustedProxyCIDRs", "rateLimits.excludedIPs", "logs.includeIP", "logs.includeAuth"]);

/** Every runtime setting the server exposes, saved as one revisioned document. */
export function SettingsView() {
  const query = useQuery(settingsQuery);
  if (query.isPending) return <Loader />;
  if (query.isError) return <ErrorState error={query.error} busy={query.isFetching} onRetry={() => void query.refetch()} />;
  // A new revision (save, reload, refresh) starts a fresh draft.
  return <SettingsForm key={query.data.revision} settings={query.data} />;
}

function SettingsForm({ settings }: { settings: RuntimeSettings }) {
  const t = useT();
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<SettingsDraft>(() => toDraft(settings));
  const [showErrors, setShowErrors] = useState(false);
  const dirty = isDirty(draft, settings);
  const errors = showErrors ? validate(draft) : {};
  const adopt = (value: RuntimeSettings) => queryClient.setQueryData(settingsQuery.queryKey, value);

  const save = useMutation({
    mutationFn: () => updateSettings(toPatch(draft, settings.revision)),
    onSuccess: (value) => {
      adopt(value);
      toast(t("system.settings.saved"));
    },
    onError: (error) => {
      if (error instanceof ApiError && error.code === "invalid_settings") setShowErrors(true);
    },
  });
  const reload = useMutation({
    mutationFn: reloadSettings,
    onSuccess: (value) => {
      adopt(value);
      toast(t("system.settings.reloaded"));
    },
    onError: (error) => toast(errorText(t, error), "danger"),
  });
  const latest = useMutation({
    mutationFn: () => queryClient.fetchQuery({ ...settingsQuery, staleTime: 0 }),
    onError: (error) => toast(errorText(t, error), "danger"),
  });

  const setValue = (path: string, value: string | boolean) => setDraft((current) => ({ ...current, values: { ...current.values, [path]: value } }));
  const setRule = (index: number, change: Partial<RuleDraft>) =>
    setDraft((current) => ({ ...current, rules: current.rules.map((rule, i) => (i === index ? { ...rule, ...change } : rule)) }));
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    // Unparsable numbers would go out as null, which the server rejects.
    if (Object.values(validate(draft)).some((error) => error.key === "system.invalid.int")) {
      setShowErrors(true);
      return;
    }
    save.mutate();
  };
  const conflict = save.error instanceof ApiError && save.error.code === "settings_conflict";
  const invalid = save.error instanceof ApiError && save.error.code === "invalid_settings";
  const rejectedPath = invalid ? Object.keys((save.error as ApiError).details ?? {})[0] : undefined;
  const fieldError = (path: string) => message(t, errors[path]);

  return (
    <form className="page-form" noValidate onSubmit={onSubmit}>
      <div className="toolbar">
        <SaveBar dirty={dirty} busy={save.isPending} onDiscard={() => { setDraft(toDraft(settings)); setShowErrors(false); save.reset(); }}>
          <Button size="sm" variant="secondary" icon={RiRefreshLine} disabled={dirty} title={dirty ? t("system.settings.reloadDirty") : undefined}
            loading={reload.isPending} onClick={() => reload.mutate()}>{t("system.settings.reload")}</Button>
        </SaveBar>
      </div>
      <div className="sys-scroll">
        {save.isError && (
          <Notice tone="danger" className="sys-notice" actions={conflict && <Button size="sm" variant="secondary" loading={latest.isPending} onClick={() => latest.mutate()}>{t("system.settings.loadLatest")}</Button>}>
            {conflict ? t("system.settings.conflict") : invalid ? (rejectedPath ? t("system.settings.invalidAt", { path: rejectedPath }) : t("system.settings.invalid")) : errorText(t, save.error)}
          </Notice>
        )}
        {sections.map((section) => (
          <FormSection key={section.id} title={t(`system.section.${section.id}` as MessageKey)} hint={t(`system.sectionHint.${section.id}` as MessageKey)}>
            <div className="sys-fields">
              {section.fields.map((spec) => (
                <SettingField key={spec.path} spec={spec} value={draft.values[spec.path]} error={fieldError(spec.path)} onChange={(value) => setValue(spec.path, value)} />
              ))}
            </div>
            {section.id === "rateLimits" && (
              <RulesEditor rules={draft.rules} errors={errors} onChange={setRule} onAdd={() => setDraft((current) => ({ ...current, rules: [...current.rules, emptyRule] }))}
                onRemove={(index) => setDraft((current) => ({ ...current, rules: current.rules.filter((_, i) => i !== index) }))} />
            )}
          </FormSection>
        ))}
      </div>
    </form>
  );
}

function message(t: Translate, error: FieldError | undefined): string | undefined {
  return error && t(error.key, error.vars);
}

function SettingField({ spec, value, error, onChange }: { spec: FieldSpec; value: string | boolean; error?: string; onChange: (value: string | boolean) => void }) {
  const t = useT();
  const text = label(t, spec.path);
  const hint = hints.has(spec.path) ? t(`system.hint.${spec.path}` as MessageKey) : undefined;
  if (spec.kind === "bool") {
    return (
      <div className="sys-check">
        <Checkbox label={text} checked={value as boolean} onChange={(event) => onChange(event.target.checked)} />
        {hint && <p className="field-hint">{hint}</p>}
      </div>
    );
  }
  const common = { label: text, hint, error, name: spec.path, value: value as string };
  if (spec.kind === "select") {
    return <Field kind="select" {...common} options={spec.options.map((option) => ({ value: option, label: t(`system.option.${option}` as MessageKey) }))} onChange={(event) => onChange(event.target.value)} />;
  }
  if (spec.kind === "lines") {
    return (
      <div className="sys-wide">
        <Field kind="textarea" {...common} mono rows={3} spellCheck={false} placeholder={spec.rule === "masked" ? "10.0.0.0/8" : "203.0.113.7"} onChange={(event) => onChange(event.target.value)} />
      </div>
    );
  }
  const numeric = spec.kind === "int";
  return <Field {...common} mono={numeric} inputMode={numeric ? "numeric" : undefined} autoComplete="off" spellCheck={false} onChange={(event) => onChange(event.target.value)} />;
}

interface RulesEditorProps {
  rules: RuleDraft[];
  errors: Record<string, FieldError>;
  onChange: (index: number, change: Partial<RuleDraft>) => void;
  onAdd: () => void;
  onRemove: (index: number) => void;
}

function RulesEditor({ rules, errors, onChange, onAdd, onRemove }: RulesEditorProps) {
  const t = useT();
  return (
    <div className="sys-rules">
      <h3>{t("system.rules.title")}</h3>
      <p className="txt-hint">{t("system.rules.hint")}</p>
      {message(t, errors["rules"]) && <p className="field-error" role="alert">{message(t, errors["rules"])}</p>}
      {rules.map((rule, index) => (
        <div key={index} className="sys-rule">
          <Field label={t("system.rules.label")} mono spellCheck={false} autoComplete="off" placeholder="POST /api/v1/collections/" value={rule.label} error={message(t, errors[`rules.${index}.label`])} onChange={(event) => onChange(index, { label: event.target.value })} />
          <Field kind="select" label={t("system.rules.audience")} value={rule.audience} options={audiences.map((value) => ({ value, label: t(`system.audience.${value || "all"}` as MessageKey) }))} onChange={(event) => onChange(index, { audience: event.target.value })} />
          <Field label={t("system.rules.duration")} mono inputMode="numeric" value={rule.duration} error={message(t, errors[`rules.${index}.duration`])} onChange={(event) => onChange(index, { duration: event.target.value })} />
          <Field label={t("system.rules.maxRequests")} mono inputMode="numeric" value={rule.maxRequests} error={message(t, errors[`rules.${index}.maxRequests`])} onChange={(event) => onChange(index, { maxRequests: event.target.value })} />
          <IconButton label={t("system.rules.remove")} icon={RiDeleteBinLine} onClick={() => onRemove(index)} />
        </div>
      ))}
      <Button variant="secondary" size="sm" icon={RiAddLine} onClick={onAdd}>{t("system.rules.add")}</Button>
    </div>
  );
}
