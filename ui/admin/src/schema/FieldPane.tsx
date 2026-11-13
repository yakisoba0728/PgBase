import { useState, type ReactNode } from "react";
import type { Collection, FieldOptions, FieldType, RelationInput } from "../api/collections";
import { Notice } from "../components/Feedback";
import { Button } from "../components/Button";
import { Checkbox, Field } from "../components/Field";
import { useT, type MessageKey } from "../i18n";
import { allowedFlags, fieldTypes, optionKeys, type DraftField } from "./draft";
import { describeErrors } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";
import { PaneHeader } from "../components/SplitView";

const int = (text: string) => (text.trim() === "" ? undefined : Math.trunc(Number(text)));

/** One value per line (commas also split). Keeps its own text so typing a separator is not swallowed. */
function ListField({ label, hint, value, onChange }: { label: string; hint?: string; value: string[] | undefined; onChange: (next: string[]) => void }) {
  const [text, setText] = useState((value ?? []).join("\n"));
  return (
    <Field
      kind="textarea"
      mono
      rows={3}
      label={label}
      hint={hint}
      value={text}
      onChange={(event) => {
        setText(event.target.value);
        onChange(event.target.value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean));
      }}
    />
  );
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <fieldset className="schema-group">
      <legend>{title}</legend>
      {children}
    </fieldset>
  );
}

function OptionFields({ field, catalog, collectionName, set, setRelation }: {
  field: DraftField;
  catalog: Collection[];
  collectionName: string;
  set: <K extends keyof FieldOptions>(key: K, value: FieldOptions[K]) => void;
  setRelation: (patch: Partial<RelationInput>) => void;
}) {
  const t = useT();
  const o = field.options ?? {};
  const keys = optionKeys[field.type];
  const has = (key: keyof FieldOptions) => keys.includes(key);
  const num = (key: "minLength" | "maxLength" | "minSelect" | "maxSelect" | "maxSize" | "maxFileSize", label: MessageKey, hint?: MessageKey) =>
    has(key) && <Field key={key} label={t(label)} hint={hint && t(hint)} type="number" min={0} mono value={o[key] ?? ""} onChange={(event) => set(key, int(event.target.value))} />;
  const str = (key: "pattern" | "autogeneratePattern" | "min" | "max", label: MessageKey, hint?: MessageKey) =>
    has(key) && <Field key={key} label={t(label)} hint={hint && t(hint)} mono spellCheck={false} value={o[key] ?? ""} onChange={(event) => set(key, event.target.value || undefined)} />;
  const isDate = field.type === "timestamptz";

  if (field.type === "relation") {
    const relation = field.relation ?? { targetCollection: "" };
    const targets = catalog.filter((collection) => collection.type !== "view").map((collection) => collection.name);
    if (collectionName && !targets.includes(collectionName)) targets.push(collectionName);
    return (
      <Group title={t("schema.options")}>
        <Field kind="select" label={t("schema.opt.target")} value={relation.targetCollection} onChange={(event) => setRelation({ targetCollection: event.target.value })}
          options={[{ value: "", label: t("schema.opt.choose") }, ...targets.map((name) => ({ value: name, label: name }))]} />
        <div className="schema-pair">
          <Field kind="select" label={t("schema.opt.cardinality")} value={relation.cardinality ?? "single"} onChange={(event) => setRelation({ cardinality: event.target.value as RelationInput["cardinality"] })}
            options={[{ value: "single", label: t("schema.opt.single") }, { value: "multiple", label: t("schema.opt.multiple") }]} />
          <Field kind="select" label={t("schema.opt.onDelete")} value={relation.onDelete ?? "set_null"} onChange={(event) => setRelation({ onDelete: event.target.value as RelationInput["onDelete"] })}
            options={(["set_null", "restrict", "cascade"] as const).map((value) => ({ value, label: t(`schema.opt.onDelete.${value}`) }))} />
        </div>
        {relation.cardinality === "multiple" && (
          <div className="schema-pair">
            <Field label={t("schema.opt.minSelect")} type="number" min={0} mono value={relation.minSelect ?? ""} onChange={(event) => setRelation({ minSelect: int(event.target.value) })} />
            <Field label={t("schema.opt.maxSelect")} type="number" min={0} mono value={relation.maxSelect ?? ""} onChange={(event) => setRelation({ maxSelect: int(event.target.value) })} />
          </div>
        )}
      </Group>
    );
  }
  if (keys.length === 0) return null;

  return (
    <Group title={t("schema.options")}>
      {(has("minLength") || has("maxLength")) && <div className="schema-pair">{num("minLength", "schema.opt.minLength")}{num("maxLength", "schema.opt.maxLength")}</div>}
      {(has("min") || has("max")) && (
        <div className="schema-pair">
          {str("min", "schema.opt.min", isDate ? "schema.opt.dateHint" : "schema.opt.decimalHint")}
          {str("max", "schema.opt.max", isDate ? "schema.opt.dateHint" : undefined)}
        </div>
      )}
      {str("pattern", "schema.opt.pattern", "schema.opt.patternHint")}
      {has("format") && (
        <Field kind="select" label={t("schema.opt.format")} value={o.format ?? ""} onChange={(event) => set("format", (event.target.value || undefined) as FieldOptions["format"])}
          options={[{ value: "", label: t("schema.opt.formatNone") }, { value: "email", label: "email" }, { value: "url", label: "url" }]} />
      )}
      {has("allowedDomains") && o.format && (
        <div className="schema-pair">
          <ListField label={t("schema.opt.allowedDomains")} value={o.allowedDomains} onChange={(value) => set("allowedDomains", value)} />
          <ListField label={t("schema.opt.deniedDomains")} value={o.deniedDomains} onChange={(value) => set("deniedDomains", value)} />
        </div>
      )}
      {str("autogeneratePattern", "schema.opt.autogenerate", "schema.opt.autogenerateHint")}
      {has("allowedValues") && <ListField label={t("schema.opt.allowedValues")} hint={t(field.type === "jsonb" ? "schema.opt.allowedValuesMulti" : "schema.opt.allowedValuesHint")} value={o.allowedValues} onChange={(value) => set("allowedValues", value)} />}
      {field.type === "jsonb" && <div className="schema-pair">{num("minSelect", "schema.opt.minSelect")}{num("maxSelect", "schema.opt.maxSelect")}</div>}
      {field.type === "file" && (
        <>
          <div className="schema-pair">{num("maxSelect", "schema.opt.maxFiles")}{num("maxFileSize", "schema.opt.maxFileSize", "schema.opt.bytes")}</div>
          <ListField label={t("schema.opt.mimeTypes")} hint={t("schema.opt.mimeHint")} value={o.mimeTypes} onChange={(value) => set("mimeTypes", value)} />
          <ListField label={t("schema.opt.thumbs")} hint={t("schema.opt.thumbsHint")} value={o.thumbs} onChange={(value) => set("thumbs", value)} />
          <Checkbox label={t("schema.opt.protected")} checked={o.protected ?? true} onChange={(event) => set("protected", event.target.checked)} />
        </>
      )}
      {num("maxSize", "schema.opt.maxSize", "schema.opt.bytes")}
      {has("convertURLs") && <Checkbox label={t("schema.opt.convertURLs")} checked={o.convertURLs ?? false} onChange={(event) => set("convertURLs", event.target.checked)} />}
      {field.type === "autodate" && (
        <div className="schema-checks">
          <Checkbox label={t("schema.opt.onCreate")} checked={o.onCreate ?? false} onChange={(event) => set("onCreate", event.target.checked)} />
          <Checkbox label={t("schema.opt.onUpdate")} checked={o.onUpdate ?? false} onChange={(event) => set("onUpdate", event.target.checked)} />
        </div>
      )}
    </Group>
  );
}

/** Detail pane for one field. Edits go straight into the draft; nothing is saved until Apply. */
export function FieldPane({ editor, field, catalog }: { editor: SchemaEditor; field: DraftField; catalog: Collection[] }) {
  const t = useT();
  const locked = Boolean(field.system);
  const errors = editor.errors.fields[field.key];
  const update = (patch: Partial<DraftField>) => editor.updateField(field.key, patch);
  const flags = allowedFlags(field.type);
  const set = <K extends keyof FieldOptions>(key: K, value: FieldOptions[K]) => update({ options: { ...field.options, [key]: value } });
  const setRelation = (patch: Partial<RelationInput>) => update({ relation: { targetCollection: "", ...field.relation, ...patch } });
  const changeType = (type: FieldType) =>
    update({
      type,
      options: type === "autodate" ? { onCreate: true } : field.options,
      relation: type === "relation" ? field.relation ?? { targetCollection: "", cardinality: "single", onDelete: "set_null" } : undefined,
      ...(allowedFlags(type).required ? {} : { required: false }),
      ...(allowedFlags(type).presentable ? {} : { presentable: false }),
      ...(allowedFlags(type).default ? {} : { defaultText: "" }),
      ...(allowedFlags(type).unique ? {} : { unique: false }),
      ...(allowedFlags(type).indexed ? {} : { indexed: false }),
    });

  return (
    <>
      <PaneHeader title={field.name || t("schema.unnamed")} mono onClose={() => editor.setPane(null)} />
      <div className="pane-body">
        {locked && <p className="field-hint schema-note">{t("schema.systemField")}</p>}
        {errors && (
          <Notice tone="danger" title={t("schema.serverRejected")}>{describeErrors(t, errors)}</Notice>
        )}
        <fieldset className="form" disabled={locked}>
          <div className="schema-pair">
            <Field label={t("schema.fieldName")} mono spellCheck={false} autoComplete="off" value={field.name} onChange={(event) => update({ name: event.target.value })}
              hint={field.id && !locked ? t("schema.renameHint") : undefined} />
            <Field kind="select" label={t("schema.fieldType")} value={field.type} onChange={(event) => changeType(event.target.value as FieldType)}
              options={fieldTypes.map((type) => ({ value: type, label: t(`schema.type.${type}`) }))} />
          </div>
          <div className="schema-checks">
            {flags.required && <Checkbox label={t("schema.flag.required")} checked={field.required} onChange={(event) => update({ required: event.target.checked })} />}
            {flags.unique && <Checkbox label={t("schema.flag.unique")} checked={field.unique} onChange={(event) => update({ unique: event.target.checked })} />}
            {flags.indexed && <Checkbox label={t("schema.flag.indexed")} checked={field.indexed} onChange={(event) => update({ indexed: event.target.checked })} />}
            <Checkbox label={t("schema.flag.hidden")} checked={field.hidden ?? false} onChange={(event) => update({ hidden: event.target.checked, presentable: event.target.checked ? false : field.presentable })} />
            {flags.presentable && <Checkbox label={t("schema.flag.presentable")} checked={field.presentable ?? false} disabled={field.hidden} onChange={(event) => update({ presentable: event.target.checked })} />}
          </div>
          <OptionFields field={field} catalog={catalog} collectionName={editor.draft.name} set={set} setRelation={setRelation} />
          {flags.default && (
            <Field label={t("schema.default")} mono spellCheck={false} placeholder={field.type === "text" ? '"draft"' : field.type === "boolean" ? "false" : "0"} hint={t("schema.defaultHint")}
              value={field.defaultText} onChange={(event) => update({ defaultText: event.target.value })} />
          )}
          <Field label={t("schema.help")} maxLength={300} value={field.help ?? ""} onChange={(event) => update({ help: event.target.value })} />
        </fieldset>
      </div>
      <footer className="pane-footer">
        {!locked && <Button variant="transparent" className="txt-danger pane-footer-start" onClick={() => editor.removeField(field.key)}>{t("schema.removeField")}</Button>}
        <Button variant="secondary" onClick={() => editor.setPane(null)}>{t("common.close")}</Button>
      </footer>
    </>
  );
}
