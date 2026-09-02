import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { CollectionDefinition, FieldDefinition } from "../backend/ports";
import { selectRelationTarget } from "../workspace/record-codec";
import {
  deselectRelationTarget,
  relationDraftSelection,
  relationHydrationBatch,
  relationHydrationFilter,
  relationHydrationLimit,
  relationTargetOption,
  relationTargetSearch,
  relationTargetSearchField,
  type RelationCandidate,
  type RelationTargetPlan,
} from "./relation-query";

export interface RelationSelection { id: string; label: string; available: boolean }
export type ActiveStep = "next" | "previous" | "first" | "last";

export interface RelationPicker {
  query: string;
  results: RelationCandidate[] | null;
  selections: RelationSelection[];
  status: string | null;
  searching: boolean;
  loadingMore: boolean;
  hasMore: boolean;
  activeIndex: number;
  searchField: FieldDefinition | undefined;
  setQuery(query: string): void;
  search(): void;
  loadMore(): void;
  select(id: string): void;
  deselect(id: string): void;
  moveActive(step: ActiveStep): void;
  chooseActive(): void;
  dismiss(): void;
}

interface PickerState {
  query: string;
  // The plan the open cursor was issued under. A continuation reuses it rather
  // than rebuilding from the query box, which the administrator may have
  // retyped since -- a cursor only decodes against its own query fingerprint.
  plan: RelationTargetPlan | null;
  cursor: string | undefined;
  results: RelationCandidate[] | null;
  status: string | null;
  busy: "search" | "more" | null;
  activeIndex: number;
  labels: ReadonlyMap<string, string>;
  missing: ReadonlySet<string>;
  claimed: ReadonlySet<string>;
}

const initialState: PickerState = {
  query: "", plan: null, cursor: undefined, results: null, status: null, busy: null,
  activeIndex: -1, labels: new Map(), missing: new Set(), claimed: new Set(),
};

function resultsStatus(count: number, more: boolean): string | null {
  if (count === 0) return "No matching target records.";
  if (!more) return null;
  return `Showing ${count} target record${count === 1 ? "" : "s"}. Load more to see the rest.`;
}

function nextActive(step: ActiveStep, current: number, count: number): number {
  if (count === 0) return -1;
  if (step === "first") return 0;
  if (step === "last") return count - 1;
  if (step === "next") return current < 0 || current + 1 >= count ? 0 : current + 1;
  return current <= 0 ? count - 1 : current - 1;
}

export function useRelationPicker(target: CollectionDefinition, field: FieldDefinition, draft: string, onChange: (value: string) => void): RelationPicker {
  const { records } = useBackend();
  const [state, setState] = useState<PickerState>(initialState);
  const patch = useCallback((next: Partial<PickerState>) => setState((current) => ({ ...current, ...next })), []);
  const pageFlight = useRef<AbortController | null>(null);
  // Hydration is not cancelled when the pending set shrinks, only when the
  // editor goes away: each batch narrows the next one, so a cleanup keyed on
  // that set would abort every request it just started.
  const hydrationFlights = useRef(new Set<AbortController>());

  useEffect(() => () => pageFlight.current?.abort(), []);
  useEffect(() => {
    const flights = hydrationFlights.current;
    return () => { for (const controller of flights) controller.abort(); flights.clear(); };
  }, []);

  const displayField = useMemo(() => relationTargetSearchField(target), [target]);
  const selected = useMemo(() => relationDraftSelection(draft), [draft]);
  const pending = useMemo(
    () => selected.slice(0, relationHydrationLimit).filter((id) => !state.claimed.has(id)),
    [selected, state.claimed],
  );
  const pendingKey = pending.join(",");

  useEffect(() => {
    const batch = pending.slice(0, relationHydrationBatch);
    if (batch.length === 0) return;
    const controller = new AbortController();
    hydrationFlights.current.add(controller);
    // Claiming the batch before the request is what stops a hydration that
    // finds nothing, or fails outright, from asking for the same ids forever.
    setState((current) => ({ ...current, claimed: new Set([...current.claimed, ...batch]) }));
    void records.list(target.name, { filter: relationHydrationFilter(batch) }, controller.signal)
      .then((page) => {
        if (controller.signal.aborted) return;
        const found = new Map<string, string>();
        for (const record of page.items) {
          const option = relationTargetOption(record, displayField);
          if (option !== undefined) found.set(option.id, option.label);
        }
        setState((current) => ({
          ...current,
          labels: new Map([...current.labels, ...found]),
          missing: new Set([...current.missing, ...batch.filter((id) => !found.has(id))]),
        }));
      })
      // A hydration that could not run leaves the id showing its own UUID,
      // which is stable and true, rather than an error the draft cannot act on.
      .catch(() => undefined)
      .finally(() => hydrationFlights.current.delete(controller));
    // `pendingKey` is the identity of `pending`; both change together.
  }, [displayField, pendingKey, records, target.name]); // eslint-disable-line react-hooks/exhaustive-deps

  const openPage = useCallback(async (plan: RelationTargetPlan, cursor: string | undefined, existing: RelationCandidate[]) => {
    pageFlight.current?.abort();
    const controller = new AbortController();
    pageFlight.current = controller;
    patch({ busy: cursor === undefined ? "search" : "more", status: null });
    try {
      const page = await records.list(target.name, { filter: plan.filter, sort: plan.sort, cursor }, controller.signal);
      if (controller.signal.aborted) return;
      const merged = [...existing];
      for (const record of page.items) {
        const option = relationTargetOption(record, plan.displayField);
        if (option !== undefined && !merged.some((candidate) => candidate.id === option.id)) merged.push(option);
      }
      patch({
        results: merged, plan, cursor: page.nextCursor, busy: null,
        activeIndex: cursor === undefined ? -1 : state.activeIndex,
        status: resultsStatus(merged.length, page.nextCursor !== undefined),
      });
    } catch {
      if (!controller.signal.aborted) patch({ busy: null, status: "Unable to search target records." });
    }
  }, [patch, records, state.activeIndex, target.name]);

  const search = useCallback(async () => {
    const request = relationTargetSearch(target, state.query);
    if ("problem" in request) {
      pageFlight.current?.abort();
      patch({
        results: null, plan: null, cursor: undefined, activeIndex: -1, busy: null,
        status: request.problem === "invalid-uuid"
          ? "Enter a canonical, non-zero target UUID to search."
          : "The target search value is too long or unsupported.",
      });
      return;
    }
    await openPage(request, undefined, []);
  }, [openPage, patch, state.query, target]);

  const select = useCallback((id: string) => {
    const selection = selectRelationTarget(field, draft, id);
    if (selection.status === "duplicate") { patch({ status: "That target is already selected." }); return; }
    if (selection.status === "max") { patch({ status: `Select no more than ${field.relation?.maxSelect ?? 0} related records.` }); return; }
    onChange(selection.draft);
    // A chosen candidate already carries its label, so the selection list shows
    // it without spending a hydration request on an id it just read.
    const label = state.results?.find((candidate) => candidate.id === id)?.label;
    setState((current) => ({
      ...current,
      status: `Selected ${label ?? id}.`,
      labels: label === undefined ? current.labels : new Map([...current.labels, [id, label]]),
      claimed: label === undefined ? current.claimed : new Set([...current.claimed, id]),
    }));
  }, [draft, field, onChange, patch, state.results]);

  return {
    query: state.query,
    results: state.results,
    selections: selected.map((id) => ({ id, label: state.labels.get(id) ?? id, available: !state.missing.has(id) })),
    status: state.status,
    searching: state.busy === "search",
    loadingMore: state.busy === "more",
    hasMore: state.cursor !== undefined,
    activeIndex: state.activeIndex,
    searchField: displayField,
    setQuery: (query) => patch({ query }),
    search: () => void search(),
    loadMore: () => {
      if (state.plan === null || state.cursor === undefined || state.busy !== null) return;
      void openPage(state.plan, state.cursor, state.results ?? []);
    },
    select,
    deselect: (id) => {
      onChange(deselectRelationTarget(draft, id));
      patch({ status: `Removed ${state.labels.get(id) ?? id}.` });
    },
    moveActive: (step) => patch({ activeIndex: nextActive(step, state.activeIndex, state.results?.length ?? 0) }),
    chooseActive: () => {
      const candidate = state.activeIndex < 0 ? undefined : state.results?.[state.activeIndex];
      if (candidate !== undefined) select(candidate.id);
    },
    dismiss: () => patch({ results: null, plan: null, cursor: undefined, activeIndex: -1, status: null }),
  };
}
