import type { CollectionField } from "../api/collections";
import { Checkbox, Field } from "../components/Field";
import { useT } from "../i18n";
import { fieldKind, jsonError, type Draft } from "./fields";

interface FieldInputProps {
  field: CollectionField;
  value: Draft;
  onChange: (value: Draft) => void;
  error?: string;
  readOnly: boolean;
}

/** One record value, edited with the control its field type calls for. */
export function FieldInput({ field, value, onChange, error, readOnly }: FieldInputProps) {
  const t = useT();
  const kind = fieldKind(field);
  const text = typeof value === "string" ? value : "";
  const shared = {
    label: field.name,
    aside: field.type,
    required: field.required && kind !== "bool",
    hint: field.help,
    error,
    disabled: readOnly,
    name: field.name,
  };
  const onText = (event: { target: { value: string } }) => onChange(event.target.value);

  switch (kind) {
    case "bool":
      return (
        <div className="field-wrap">
          <Checkbox label={field.name} checked={value === true} disabled={readOnly} onChange={(event) => onChange(event.target.checked)} />
          {error && <p className="field-error" role="alert">{error}</p>}
        </div>
      );
    case "number":
      return <Field {...shared} value={text} onChange={onText} inputMode="decimal" autoComplete="off" spellCheck={false} mono />;
    case "email":
    case "url":
      return <Field {...shared} type={kind} value={text} onChange={onText} />;
    case "editor":
      return <Field {...shared} kind="textarea" value={text} onChange={onText} rows={6} />;
    case "json": {
      const invalid = jsonError(text);
      return <Field {...shared} kind="textarea" mono value={text} onChange={onText} rows={5} spellCheck={false} error={error ?? (invalid ? t("validation.invalid_json") : undefined)} />;
    }
    case "select": {
      const options = (field.options?.allowedValues ?? []).map((option) => ({ value: option, label: option }));
      return <Field {...shared} kind="select" value={text} onChange={onText} options={[{ value: "", label: "" }, ...options]} />;
    }
    case "multiselect": {
      const options = (field.options?.allowedValues ?? []).map((option) => ({ value: option, label: option }));
      const onSelect = (event: { target: HTMLSelectElement }) => onChange(Array.from(event.target.selectedOptions, (option) => option.value));
      return <Field {...shared} kind="select" multiple value={Array.isArray(value) ? value : []} onChange={onSelect} options={options} size={Math.min(6, options.length)} />;
    }
    case "date":
      return <Field {...shared} aside={`${field.type} · UTC`} type="datetime-local" step={1} value={text} onChange={onText} />;
    case "relation":
    case "relations":
      return (
        <Field
          {...shared}
          mono
          value={text}
          onChange={onText}
          spellCheck={false}
          autoComplete="off"
          hint={field.help ?? (kind === "relations" ? t("records.relationsHint") : t("records.relationHint"))}
        />
      );
    case "password":
      return <Field {...shared} type="password" value={text} onChange={onText} autoComplete="new-password" required={false} hint={field.help ?? t("records.passwordHint")} />;
    default:
      return <Field {...shared} value={text} onChange={onText} />;
  }
}
