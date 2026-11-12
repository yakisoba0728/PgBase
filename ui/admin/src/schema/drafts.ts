import { stringifyJson } from "../api/http";
import { createStore } from "../app/store";
import type { Draft } from "./draft";

interface Entry {
  base: Draft;
  draft: Draft;
}

// Drafts live outside React (and outside the schema chunk) so switching between the collection's tabs keeps them.
export const drafts = createStore<Record<string, Entry>>({});

export function dropDraft(key: string) {
  const { [key]: _dropped, ...rest } = drafts.get();
  drafts.set(rest);
}

export const isDirty = (entry: Entry | undefined) => Boolean(entry) && stringifyJson(entry!.draft) !== stringifyJson(entry!.base);
