import { t } from "../i18n";
import type { DraftEdit, DraftField } from "../designer/collection-designer";
import type { DraftFieldOptions } from "../designer/field-options";
import { useState } from "react";

export function FieldOptionsEditor({ field, index, onEdit, idPrefix = "field" }: { idPrefix?: string; field: DraftField; index: number; onEdit(edit: DraftEdit): void }) {
  const options = field.options ?? {};
  const [expanded, setExpanded] = useState(() => Object.values(options).some(value => value !== undefined && value !== ""));
  const editor = field.type === "editor", autodate = field.type === "autodate", password = field.type === "password";
  const text = field.type === "text";
  const multiple = field.type === "jsonb";
  const numeric = ["integer", "bigint", "numeric"].includes(field.type);
  const file = field.type === "file";
	if (!text && !multiple && !numeric && !file && !editor && !autodate && !password) return null;
  const edit = (key: keyof DraftFieldOptions, value: string) => onEdit({ set: "options", value: { ...options, [key]: value } });
  const input = (key: keyof DraftFieldOptions, label: string, count = false, help?: string) => {
    const id = `${idPrefix}-${index}-option-${key}`;
    return <div className="form-control" key={key}><label htmlFor={id}>{t("Field {number} {label}", { number: index + 1, label: t(label) })}</label><input id={id} aria-describedby={help ? `${id}-help` : undefined} type={count ? "number" : "text"} min={count ? 0 : undefined} step={count ? 1 : undefined} value={options[key] ?? ""} onChange={event => edit(key, event.target.value)} />{help && <p id={`${id}-help`}>{t(help)}</p>}</div>;
  };
  const textArea = (key: keyof DraftFieldOptions, label: string, help: string) => {
    const id = `${idPrefix}-${index}-option-${key}`;
    return <div className="form-control option-full"><label htmlFor={id}>{t("Field {number} {label}", { number: index + 1, label: t(label) })}</label><textarea id={id} rows={2} aria-describedby={`${id}-help`} value={options[key] ?? ""} onChange={event => edit(key, event.target.value)} /><p id={`${id}-help`}>{t(help)}</p></div>;
  };
  return <details className="field-options" open={expanded} onToggle={event => setExpanded(event.currentTarget.open)} onInvalidCapture={event => { event.currentTarget.open = true; }}>
    <summary>{t("Validation")} <span>{t("Limits, formats and allowed values")}</span></summary>
    <fieldset className="field-validation" disabled={field.system}><legend className="sr-only">{t("Validation")}</legend>
    {editor && <>{input("maxSize", "maximum HTML bytes", true)}<p>{t("Default: 5 MiB. Indexed or unique editors require a limit of at most 2,048 bytes.")}</p><label>{t("Field {number} strip local URL domain", { number: index + 1 })}<input type="checkbox" checked={options.convertURLs === "true"} onChange={event => edit("convertURLs", String(event.target.checked))} /></label></>}
    {autodate && <><label>{t("Field {number} set on create", { number: index + 1 })}<input type="checkbox" checked={options.onCreate === "true"} onChange={event => edit("onCreate", String(event.target.checked))} /></label><label>{t("Field {number} set on update", { number: index + 1 })}<input type="checkbox" checked={options.onUpdate === "true"} onChange={event => edit("onUpdate", String(event.target.checked))} /></label><p>{t("Enable at least one event. Values are set by the server.")}</p></>}
    {password && <>{input("minLength", "minimum characters", true)}{input("maxLength", "maximum characters", true)}{input("pattern", "pattern")}<p>{t("Default maximum: 71 Unicode characters. Pattern uses Go regular expressions. Stored passwords are never returned.")}</p></>}
    {text && <>
      {input("minLength", "minimum characters", true)}{input("maxLength", "maximum characters", true)}
      {input("pattern", "pattern", false, t("Pattern uses Go regular expressions. Use ^ and $ to require a full match."))}
      {input("autogeneratePattern", "autogenerate pattern", false, t("When omitted on create, generate a random matching value. Explicit values win; updates never generate."))}
      <label>{t("Field {number} format", { number: index + 1 })}<select value={options.format ?? ""} onChange={event => { const format = event.target.value; onEdit({ set: "options", value: { ...options, format, allowedDomains: format ? options.allowedDomains : "", deniedDomains: format ? options.deniedDomains : "" } }); }}><option value="">{t("Any text")}</option><option value="email">{t("Email")}</option><option value="url">{t("HTTP / HTTPS URL")}</option></select></label>
      {options.format && <>{textArea("allowedDomains", "allowed domains", t("One domain per line; exact hosts and subdomains match."))}{textArea("deniedDomains", "denied domains", t("One domain per line. Denied domains override allowed domains."))}</>}
    </>}
    {(text || multiple) && textArea("allowedValues", "allowed values", `${t("Enter a JSON string array, such as [\"draft\", \"published\"]. Empty means unrestricted.")} ${multiple ? t("With choices configured, JSON must be an array of unique selected strings.") : t("Each non-null value must match a choice exactly.")}`)}
    {multiple && <>{input("minSelect", "minimum selections", true)}{input("maxSelect", "maximum selections", true)}</>}
    {numeric && <>{input("min", "minimum value")}{input("max", "maximum value")}<p>{t("Use decimal text to preserve large integer and decimal precision.")}</p></>}
    {file && <>
      {input("maxFileSize", "maximum bytes per file", true)}{input("maxSelect", "maximum files", true)}
      {textArea("mimeTypes", "MIME types", t("One exact type or wildcard per line, such as image/*."))}
      {textArea("thumbs", "thumbnails", t("One size per line: WxH, WxHt, WxHb, WxHf, 0xH, or Wx0."))}
    </>}
  </fieldset></details>;
}
