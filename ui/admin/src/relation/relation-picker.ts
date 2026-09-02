import { useCallback, useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { CollectionDefinition, FieldDefinition } from "../backend/ports";
import { selectRelationTarget } from "../workspace/record-codec";
import { relationTargetOption, relationTargetSearch, relationTargetSearchField, type RelationCandidate } from "./relation-query";

export interface RelationPicker {
  query: string;
  results: RelationCandidate[] | null;
  status: string | null;
  searching: boolean;
  searchField: FieldDefinition | undefined;
  setQuery(query: string): void;
  search(): void;
  select(id: string): void;
}

export function useRelationPicker(target: CollectionDefinition, field: FieldDefinition, draft: string, onChange: (value: string) => void): RelationPicker {
  const { records } = useBackend();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<RelationCandidate[] | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const inFlight = useRef<AbortController | null>(null);

  useEffect(() => () => inFlight.current?.abort(), []);

  const search = useCallback(async () => {
    inFlight.current?.abort();
    inFlight.current = null;
    setSearching(false);
    const request = relationTargetSearch(target, query);
    if ("problem" in request) {
      setResults(null);
      setStatus(request.problem === "invalid-uuid"
        ? "Enter a canonical, non-zero target UUID to search."
        : "The target search value is too long or unsupported.");
      return;
    }
    const controller = new AbortController();
    inFlight.current = controller;
    setSearching(true);
    setStatus(null);
    try {
      const page = await records.list(target.name, { filter: request.filter, sort: request.sort }, controller.signal);
      if (controller.signal.aborted) return;
      const options = page.items.flatMap((record) => {
        const option = relationTargetOption(record, request.displayField);
        return option === undefined ? [] : [option];
      });
      setResults(options);
      setStatus(options.length === 0 ? "No matching target records."
        : page.nextCursor ? "Showing the first 50 matching target records." : null);
    } catch {
      if (!controller.signal.aborted) {
        setResults(null);
        setStatus("Unable to search target records.");
      }
    } finally {
      if (!controller.signal.aborted) setSearching(false);
    }
  }, [query, records, target]);

  return {
    query,
    results,
    status,
    searching,
    searchField: relationTargetSearchField(target),
    setQuery,
    search: () => void search(),
    select: (id) => {
      const selection = selectRelationTarget(field, draft, id);
      if (selection.status === "selected") { onChange(selection.draft); setStatus(`Selected ${id}.`); }
      else if (selection.status === "duplicate") setStatus("That target is already selected.");
      else setStatus(`Select no more than ${field.relation?.maxSelect ?? 0} related records.`);
    },
  };
}
