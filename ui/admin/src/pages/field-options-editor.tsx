import type { DraftEdit, DraftField } from "../designer/collection-designer";
import type { DraftFieldOptions } from "../designer/field-options";

export function FieldOptionsEditor({ field, index, onEdit }: { field: DraftField; index: number; onEdit(edit: DraftEdit): void }) {
  const options = field.options ?? {};
  const text = field.type === "text";
  const multiple = field.type === "jsonb";
  const numeric = ["integer", "bigint", "numeric"].includes(field.type);
  const file = field.type === "file";
	if (!text && !multiple && !numeric && !file) return null;
  const edit = (key: keyof DraftFieldOptions, value: string) => onEdit({ set: "options", value: { ...options, [key]: value } });
  const input = (key: keyof DraftFieldOptions, label: string, count = false) => <label key={key}>Field {index + 1} {label}<input type={count ? "number" : "text"} min={count ? 0 : undefined} step={count ? 1 : undefined} value={options[key] ?? ""} onChange={event => edit(key, event.target.value)} /></label>;
  return <fieldset><legend>Validation</legend>
    {text && <>
      {input("minLength", "minimum characters", true)}{input("maxLength", "maximum characters", true)}
      {input("pattern", "pattern")}
      <p>Pattern uses Go regular expressions. Use ^ and $ to require a full match.</p>
      <label>Field {index + 1} format<select value={options.format ?? ""} onChange={event => { const format = event.target.value; onEdit({ set: "options", value: { ...options, format, allowedDomains: format ? options.allowedDomains : "" } }); }}><option value="">Any text</option><option value="email">Email</option><option value="url">HTTP / HTTPS URL</option></select></label>
      {options.format && <label>Field {index + 1} allowed domains<textarea value={options.allowedDomains ?? ""} onChange={event => edit("allowedDomains", event.target.value)} /><span>One domain per line; subdomains are included.</span></label>}
    </>}
    {(text || multiple) && <label>Field {index + 1} allowed values<textarea value={options.allowedValues ?? ""} onChange={event => edit("allowedValues", event.target.value)} /><span>Enter a JSON string array, such as ["draft", "published"]. Empty means unrestricted. {multiple ? "With choices configured, JSON must be an array of unique selected strings." : "Each non-null value must match a choice exactly."}</span></label>}
    {multiple && <>{input("minSelect", "minimum selections", true)}{input("maxSelect", "maximum selections", true)}</>}
    {numeric && <>{input("min", "minimum value")}{input("max", "maximum value")}<p>Use decimal text to preserve large integer and decimal precision.</p></>}
    {file && <>
      {input("maxFileSize", "maximum bytes per file", true)}{input("maxSelect", "maximum files", true)}
      <label>Field {index + 1} MIME types<textarea value={options.mimeTypes ?? ""} onChange={event => edit("mimeTypes", event.target.value)} /><span>One exact type or wildcard per line, such as image/*.</span></label>
      <label>Field {index + 1} thumbnails<textarea value={options.thumbs ?? ""} onChange={event => edit("thumbs", event.target.value)} /><span>One size per line: WxH, WxHt, WxHb, WxHf, 0xH, or Wx0.</span></label>
    </>}
  </fieldset>;
}
