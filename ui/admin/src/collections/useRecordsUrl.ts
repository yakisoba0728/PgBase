import { useMemo } from "react";
import { useSearchState } from "../app/search";
import type { CollectionField } from "../api/collections";

/** Header click cycle for one column: ascending, descending, unsorted. */
export function nextSort(current: string, field: string): string {
  if (current === field) return `-${field}`;
  if (current === `-${field}`) return "";
  return field;
}

// Keeps the generated expression well inside the server's filter size and node limits on wide collections.
const maxSearchFields = 30;

/** Plain words search the text fields case-insensitively (presentable and indexed first, at most 30);
 * anything with an operator is sent as a filter expression. */
export function buildFilter(input: string, fields: CollectionField[]): string {
  const text = input.trim();
  // A quote alone (O'Brien) is a search word; expressions always carry an operator.
  if (text === "" || /[=~<>!&|()]/.test(text)) return text;
  const quoted = (value: string) => `'${value.replaceAll("'", "''")}'`;
  const searchable = fields
    .filter((field) => (field.type === "text" || field.type === "editor") && !field.hidden)
    .sort((a, b) => Number(b.presentable) - Number(a.presentable) || Number(b.indexed || b.unique) - Number(a.indexed || a.unique))
    .slice(0, maxSearchFields);
  if (searchable.length === 0) return `id = ${quoted(text)}`;
  return searchable.map((field) => `${field.name}:lower ~ ${quoted(text.toLowerCase())}`).join(" || ");
}

/** Records view state kept in the query string: filter, sort and the open record. */
export function useRecordsUrl() {
  const { params, update } = useSearchState();
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
      /** `replace` for updates driven by typing, so each keystroke is not a history entry. */
      setFilter: (value: string, replace?: boolean) => update({ filter: value.trim() || null }, { replace }),
      /** Recovery from a rejected query: the sort can be the part the server refused. */
      clearQuery: () => update({ filter: null, sort: null }),
      toggleSort: (field: string) => update({ sort: nextSort(sort, field) || null }),
      open: (id: string) => update({ record: id, new: null }),
      create: () => update({ record: null, new: "1" }),
      close: () => update({ record: null, new: null }),
    }),
    [filter, sort, record, creating, update],
  );
}
