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
    return <div className="form-control" key={key}><label htmlFor={id}>Field {index + 1} {label}</label><input id={id} aria-describedby={help ? `${id}-help` : undefined} type={count ? "number" : "text"} min={count ? 0 : undefined} step={count ? 1 : undefined} value={options[key] ?? ""} onChange={event => edit(key, event.target.value)} />{help && <p id={`${id}-help`}>{help}</p>}</div>;
  };
  const textArea = (key: keyof DraftFieldOptions, label: string, help: string) => {
    const id = `${idPrefix}-${index}-option-${key}`;
    return <div className="form-control option-full"><label htmlFor={id}>Field {index + 1} {label}</label><textarea id={id} rows={2} aria-describedby={`${id}-help`} value={options[key] ?? ""} onChange={event => edit(key, event.target.value)} /><p id={`${id}-help`}>{help}</p></div>;
  };
  return <details className="field-options" open={expanded} onToggle={event => setExpanded(event.currentTarget.open)} onInvalidCapture={event => { event.currentTarget.open = true; }}>
    <summary>Validation <span>Limits, formats and allowed values</span></summary>
    <fieldset className="field-validation" disabled={field.system}><legend className="sr-only">Validation</legend>
    {editor && <>{input("maxSize", "maximum HTML bytes", true)}<p>Default: 5 MiB. Indexed or unique editors require a limit of at most 2,048 bytes.</p><label>Field {index + 1} strip local URL domain<input type="checkbox" checked={options.convertURLs === "true"} onChange={event => edit("convertURLs", String(event.target.checked))} /></label></>}
    {autodate && <><label>Field {index + 1} set on create<input type="checkbox" checked={options.onCreate === "true"} onChange={event => edit("onCreate", String(event.target.checked))} /></label><label>Field {index + 1} set on update<input type="checkbox" checked={options.onUpdate === "true"} onChange={event => edit("onUpdate", String(event.target.checked))} /></label><p>Enable at least one event. Values are set by the server.</p></>}
    {password && <>{input("minLength", "minimum characters", true)}{input("maxLength", "maximum characters", true)}{input("pattern", "pattern")}<p>Default maximum: 71 Unicode characters. Pattern uses Go regular expressions. Stored passwords are never returned.</p></>}
    {text && <>
      {input("minLength", "minimum characters", true)}{input("maxLength", "maximum characters", true)}
      {input("pattern", "pattern", false, "Pattern uses Go regular expressions. Use ^ and $ to require a full match.")}
      {input("autogeneratePattern", "autogenerate pattern", false, "When omitted on create, generate a random matching value. Explicit values win; updates never generate.")}
      <label>Field {index + 1} format<select value={options.format ?? ""} onChange={event => { const format = event.target.value; onEdit({ set: "options", value: { ...options, format, allowedDomains: format ? options.allowedDomains : "", deniedDomains: format ? options.deniedDomains : "" } }); }}><option value="">Any text</option><option value="email">Email</option><option value="url">HTTP / HTTPS URL</option></select></label>
      {options.format && <>{textArea("allowedDomains", "allowed domains", "One domain per line; exact hosts and subdomains match.")}{textArea("deniedDomains", "denied domains", "One domain per line. Denied domains override allowed domains.")}</>}
    </>}
    {(text || multiple) && textArea("allowedValues", "allowed values", `Enter a JSON string array, such as ["draft", "published"]. Empty means unrestricted. ${multiple ? "With choices configured, JSON must be an array of unique selected strings." : "Each non-null value must match a choice exactly."}`)}
    {multiple && <>{input("minSelect", "minimum selections", true)}{input("maxSelect", "maximum selections", true)}</>}
    {numeric && <>{input("min", "minimum value")}{input("max", "maximum value")}<p>Use decimal text to preserve large integer and decimal precision.</p></>}
    {file && <>
      {input("maxFileSize", "maximum bytes per file", true)}{input("maxSelect", "maximum files", true)}
      {textArea("mimeTypes", "MIME types", "One exact type or wildcard per line, such as image/*.")}
      {textArea("thumbs", "thumbnails", "One size per line: WxH, WxHt, WxHb, WxHf, 0xH, or Wx0.")}
    </>}
  </fieldset></details>;
}
