import type { FieldOptions } from "../backend/ports";

export type DraftFieldOptions = Partial<Record<keyof FieldOptions, string>>;
const counts = ["minLength", "maxLength", "minSelect", "maxSelect", "maxFileSize", "maxSize"] as const;

export function draftFieldOptions(value?: FieldOptions): DraftFieldOptions | undefined {
  if (!value) return undefined;
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, Array.isArray(item) ? key === "allowedValues" ? JSON.stringify(item) : item.join("\n") : String(item)]));
}

export function fieldOptionsInput(draft?: DraftFieldOptions): FieldOptions | undefined {
  if (!draft) return undefined;
  const result: FieldOptions = {};
  for (const key of counts) {
    const value = draft[key];
    if (value === undefined || value === "") continue;
    if (!/^(0|[1-9][0-9]*)$/.test(value) || !Number.isSafeInteger(Number(value))) throw new Error(`${key} must be a non-negative integer.`);
    result[key] = Number(value);
  }
  for (const key of ["convertURLs", "onCreate", "onUpdate"] as const) {
    if (draft[key] === undefined || draft[key] === "") continue;
    if (draft[key] !== "true" && draft[key] !== "false") throw new Error(`${key} must be true or false.`);
    result[key] = draft[key] === "true";
  }
  for (const key of ["pattern", "autogeneratePattern", "min", "max"] as const) if (draft[key]) result[key] = draft[key];
  if (draft.format) {
    if (draft.format !== "email" && draft.format !== "url") throw new Error("Choose email or URL format.");
    result.format = draft.format;
  }
  if (draft.allowedDomains) result.allowedDomains = draft.allowedDomains.split("\n").filter(value => value !== "");
  if (draft.deniedDomains) result.deniedDomains = draft.deniedDomains.split("\n").filter(value => value !== "");
  if (draft.mimeTypes) result.mimeTypes = draft.mimeTypes.split("\n").filter(value => value !== "");
  if (draft.thumbs) result.thumbs = draft.thumbs.split("\n").filter(value => value !== "");
  if (draft.allowedValues) {
    let values: unknown;
    try { values = JSON.parse(draft.allowedValues); } catch { throw new Error('Allowed values must be a JSON string array, for example ["draft", "published"].'); }
    if (!Array.isArray(values) || values.some(value => typeof value !== "string")) throw new Error("Allowed values must contain only strings.");
    result.allowedValues = values;
  }
  if (result.minLength !== undefined && result.maxLength !== undefined && result.minLength > result.maxLength) throw new Error("Minimum length exceeds maximum length.");
  if (result.minSelect !== undefined && result.maxSelect !== undefined && result.minSelect > result.maxSelect) throw new Error("Minimum selections exceeds maximum selections.");
  return Object.keys(result).length ? result : undefined;
}
