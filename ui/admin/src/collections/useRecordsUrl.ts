import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router";
import type { CollectionField } from "../api/collections";

/** Header click cycle for one column: ascending, descending, unsorted. */
export function nextSort(current: string, field: string): string {
  if (current === field) return `-${field}`;
  if (current === `-${field}`) return "";
  return field;
}

/** Plain words search the text fields; anything with an operator is sent as a filter expression. */
export function buildFilter(input: string, fields: CollectionField[]): string {
  const text = input.trim();
  if (text === "" || /[=~<>!&|()']/.test(text)) return text;
  const searchable = fields.filter((field) => field.type === "text" || field.type === "editor");
  if (searchable.length === 0) return `id = '${text}'`;
  return searchable.map((field) => `${field.name} ~ '${text}'`).join(" || ");
}

/** Records view state kept in the query string: filter, sort and the open record. */
export function useRecordsUrl() {
  const [params, setParams] = useSearchParams();
  const update = useCallback(
    (changes: Record<string, string | null>) =>
      setParams((previous) => {
        const next = new URLSearchParams(previous);
        for (const [key, value] of Object.entries(changes)) {
          if (value) next.set(key, value);
          else next.delete(key);
        }
        return next;
      }),
    [setParams],
  );
  const filter = params.get("filter") ?? "";
  const sort = params.get("sort") ?? "";
  const record = params.get("record");
  // A separate flag, since "new" could be a real text record id.
  const creating = params.has("new");
  return useMemo(
    () => ({
      filter,
      sort,
      record,
      creating,
      setFilter: (value: string) => update({ filter: value.trim() || null }),
      toggleSort: (field: string) => update({ sort: nextSort(sort, field) || null }),
      open: (id: string) => update({ record: id, new: null }),
      create: () => update({ record: null, new: "1" }),
      close: () => update({ record: null, new: null }),
    }),
    [filter, sort, record, creating, update],
  );
}
