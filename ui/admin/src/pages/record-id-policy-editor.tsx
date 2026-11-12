import { t } from "../i18n";
import type { RecordIDPolicy } from "../backend/ports";

const defaultTextPolicy: RecordIDPolicy = {
  kind: "text", minLength: 15, maxLength: 15,
  pattern: "^[a-z0-9]+$", autogeneratePattern: "[a-z0-9]{15}",
};

export function RecordIDPolicyEditor({ value, locked, disabled, onChange }: {
  value?: RecordIDPolicy;
  locked: boolean;
  disabled: boolean;
  onChange(value?: RecordIDPolicy): void;
}) {
  const text = value?.kind === "text";
  const update = (patch: Partial<RecordIDPolicy>) => onChange({ ...defaultTextPolicy, ...value, ...patch });
  return <fieldset className="record-identity" disabled={disabled || locked}>
    <legend>{t("Record identity")}</legend>
    <label>{t("Record ID kind")} <select value={value?.kind ?? "uuid"} onChange={(event) => onChange(event.target.value === "text" ? { ...defaultTextPolicy } : undefined)}>
      <option value="uuid">{t("UUID")}</option><option value="text">{t("Text")}</option>
    </select></label>
    {text && <>
      <label>{t("Record ID minimum length")} <input type="number" min={1} max={255} required value={value.minLength ?? ""} onChange={(event) => update({ minLength: event.target.value === "" ? undefined : Number(event.target.value) })} /></label>
      <label>{t("Record ID maximum length")} <input type="number" min={1} max={255} required value={value.maxLength ?? ""} onChange={(event) => update({ maxLength: event.target.value === "" ? undefined : Number(event.target.value) })} /></label>
      <label>{t("Record ID pattern")} <input required value={value.pattern ?? ""} onChange={(event) => update({ pattern: event.target.value })} /></label>
      <label>{t("Record ID generation pattern")} <input value={value.autogeneratePattern ?? ""} onChange={(event) => update({ autogeneratePattern: event.target.value })} /></label>
      <p>{t("Text IDs preserve case. The server validates the patterns and every ID. Leave the generation pattern empty to require an ID when creating records.")}</p>
    </>}
    {locked && <p>{t("The record ID policy cannot be changed after the collection is created.")}</p>}
  </fieldset>;
}
