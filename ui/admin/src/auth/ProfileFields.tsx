import type { AuthCollectionField } from "../api/authCollections";
import { Checkbox, Field } from "../components/Field";
import type { Translate } from "../i18n";
import type { ProfileDraft, Problems } from "./forms";
import { problemText } from "./useDraft";

interface ProfileFieldsProps {
  t: Translate;
  fields: AuthCollectionField[];
  draft: ProfileDraft;
  problems: Problems;
  onChange: (name: string, value: string | boolean) => void;
}

/** Inputs for an auth collection's own fields; structured types are edited as JSON. */
export function ProfileFields({ t, fields, draft, problems, onChange }: ProfileFieldsProps) {
  return fields.map((field) => {
    const value = field.type === "boolean" ? draft[field.name] === true : String(draft[field.name] ?? "");
    if (typeof value === "boolean") return <Checkbox key={field.name} label={field.name} checked={value} onChange={(event) => onChange(field.name, event.target.checked)} />;
    const shared = { label: field.name, aside: field.type, required: field.required, value, hint: field.type === "file" ? t("auth.user.fileReferences") : field.help, error: problemText(t, problems, field.name) };
    if (field.type === "file" || field.type === "jsonb" || field.type === "geoPoint" || field.type === "relation" || field.type === "editor") {
      return <Field key={field.name} kind="textarea" rows={3} mono={field.type !== "editor"} spellCheck={false} {...shared} onChange={(event) => onChange(field.name, event.target.value)} />;
    }
    const numeric = field.type === "integer" || field.type === "bigint" || field.type === "numeric";
    return <Field key={field.name} type={field.type === "password" ? "password" : "text"} autoComplete={field.type === "password" ? "new-password" : undefined} mono={numeric || field.type === "uuid"} inputMode={numeric ? "decimal" : undefined} {...shared} onChange={(event) => onChange(field.name, event.target.value)} />;
  });
}
