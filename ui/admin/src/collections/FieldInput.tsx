import { lazy, Suspense, useId, useRef } from "react";
import type { CollectionField } from "../api/collections";
import type { RecordData } from "../api/records";
import { Checkbox, Field } from "../components/Field";
import { Skeleton } from "../components/Feedback";
import { useT } from "../i18n";
import { fieldKind, jsonError, type Draft } from "./fields";
import { GeoPointInput } from "./GeoPointInput";
import { RelationPicker } from "./RelationPicker";

// Tiptap is large; it loads the first time an editor field is shown.
const RichText = lazy(() => import("./RichText"));

interface FieldInputProps {
  field: CollectionField;
  value: Draft;
  onChange: (value: Draft) => void;
  error?: string;
  readOnly: boolean;
  onPendingChange?: (pending: boolean) => void;
  /** Expand data of the open record, for relation chip labels. */
  expand?: RecordData["expand"];
}

/** One record value, edited with the control its field type calls for. */
export function FieldInput({ field, value, onChange, error, readOnly, expand, onPendingChange }: FieldInputProps) {
  const t = useT();
  const id = useId();
  const kind = fieldKind(field);
  const text = typeof value === "string" ? value : "";
  // Text with a line break edits in a textarea, and stays one so deleting the break does not swap the control under the caret.
  const multiline = useRef(false);
  if (field.type === "text" && text.includes("\n")) multiline.current = true;
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
      return (
        <div className="field-wrap">
          <div className="field" data-invalid={error ? "" : undefined}>
            <label className="field-label" htmlFor={id}>
              {field.name}
              {field.required && <span className="field-required" aria-hidden>*</span>}
              <span className="mono">{field.type}</span>
            </label>
            <Suspense fallback={<Skeleton variant="editor" />}>
              <RichText id={id} label={field.name} value={text} onChange={onChange} readOnly={readOnly} invalid={Boolean(error)} convertURLs={field.options?.convertURLs} />
            </Suspense>
          </div>
          {(error || field.help) && <p className={error ? "field-error" : "field-hint"} role={error ? "alert" : undefined}>{error ?? field.help}</p>}
        </div>
      );
    case "json": {
      const invalid = jsonError(text);
      return <Field {...shared} kind="textarea" mono value={text} onChange={onText} rows={5} spellCheck={false} error={error ?? (invalid ? t("validation.invalid_json") : undefined)} />;
    }
    case "geo":
      return <GeoPointInput field={field} value={Array.isArray(value) ? value : []} onChange={onChange} error={error} readOnly={readOnly} />;
    case "select": {
      const options = (field.options?.allowedValues ?? []).map((option) => ({ value: option, label: option }));
      return <Field {...shared} kind="select" value={text} onChange={onText} options={[{ value: "", label: t("common.na") }, ...options]} />;
    }
    case "multiselect": {
      const chosen = Array.isArray(value) ? value : [];
      const allowed = field.options?.allowedValues ?? [];
      // In allowed-value order, as the list box gave it, so unchecking and rechecking restores the same array.
      const toggle = (option: string, on: boolean) => onChange(allowed.filter((item) => (item === option ? on : chosen.includes(item))));
      return (
        <div className="field-wrap">
          <div className="field" data-invalid={error ? "" : undefined}>
            <span className="field-label" id={id}>
              {field.name}
              {field.required && <span className="field-required" aria-hidden>*</span>}
              <span className="mono">{field.type}</span>
            </span>
            <div className="field-checks" role="group" aria-labelledby={id} aria-describedby={error || field.help ? `${id}-note` : undefined}>
              {allowed.map((option) => (
                <Checkbox key={option} label={option} checked={chosen.includes(option)} disabled={readOnly} onChange={(event) => toggle(option, event.target.checked)} />
              ))}
            </div>
          </div>
          {(error || field.help) && <p id={`${id}-note`} className={error ? "field-error" : "field-hint"} role={error ? "alert" : undefined}>{error ?? field.help}</p>}
        </div>
      );
    }
    case "date":
      return <Field {...shared} aside={`${field.type} · UTC`} type="datetime-local" step={1} value={text} onChange={onText} />;
    case "relation":
    case "relations": {
      const related = expand?.[field.name];
      const known = Array.isArray(related) ? related : related ? [related] : [];
      const ids = Array.isArray(value) ? value : text ? [text] : [];
      return <RelationPicker onPendingChange={onPendingChange} field={field} value={ids} known={known} onChange={(next) => onChange(kind === "relations" ? next : (next[0] ?? ""))} error={error} readOnly={readOnly} />;
    }
    case "password":
      return <Field {...shared} type="password" value={text} onChange={onText} autoComplete="new-password" required={false} hint={field.help ?? t("records.passwordHint")} />;
    default:
      if (kind === "text" && multiline.current) return <Field {...shared} kind="textarea" value={text} onChange={onText} />;
      return <Field {...shared} value={text} onChange={onText} />;
  }
}
