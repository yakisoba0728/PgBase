import { Skeleton } from "../components/Feedback";
import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { Link } from "react-router";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiCloseLine, RiExternalLinkLine, RiSearchLine } from "@remixicon/react";
import type { CollectionField } from "../api/collections";
import { ApiError } from "../api/http";
import { createRecord, listRecords, type RecordData } from "../api/records";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { collectionsQuery } from "./queries";
import { idsFilter, labelField, recordLabel, relationSearchFilter } from "./relation";
import { randomKey } from "./useRecordForm";

interface RelationPickerProps {
  field: CollectionField;
  /** Selected ids in order (single relations hold at most one). */
  value: string[];
  onChange: (value: string[]) => void;
  /** Records the caller already has (e.g. list expand data), to label chips without a request. */
  known?: RecordData[];
  error?: string;
  readOnly: boolean;
  onPendingChange?: (pending: boolean) => void;
}

const pageSize = 20;

function useDebounced<T>(value: T, delay: number): T {
  const [settled, setSettled] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return settled;
}

/** Picks related records by searching the target collection; selections show as labelled chips. */
export function RelationPicker({ field, value, onChange, known = [], error, readOnly, onPendingChange }: RelationPickerProps) {
  const t = useT();
  const id = useId();
  const queryClient = useQueryClient();
  const catalog = useQuery(collectionsQuery).data;
  const target = catalog?.find((candidate) => candidate.id === field.relation?.targetCollectionId);
  const multiple = field.relation?.cardinality === "multiple";
  const max = multiple ? field.relation?.maxSelect || Infinity : 1;
  const [text, setText] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  const query = useDebounced(text, 250);
  const label = target ? labelField(target) : undefined;
  const filter = target ? relationSearchFilter(target, query) : null;

  // Labels for selected ids the caller did not already have.
  const pickedKey = (selected: string) => ["records", target?.name, "picked", selected];
  const missing = value.filter((selected) => !known.some((record) => record.id === selected) && !queryClient.getQueryData(pickedKey(selected)));
  const labels = useQuery({
    queryKey: ["records", target?.name, "labels", missing],
    queryFn: ({ signal }) => listRecords(target!.name, { filter: idsFilter(missing), limit: Math.min(200, missing.length), signal }),
    enabled: Boolean(target) && missing.length > 0 && missing.length <= 50,
    staleTime: 60_000,
  });
  const lookup = new Map([...known, ...(labels.data?.items ?? [])].map((record) => [record.id, record]));

  const results = useInfiniteQuery({
    queryKey: ["records", target?.name, "pick", filter],
    queryFn: ({ pageParam, signal }) => listRecords(target!.name, { filter: filter || undefined, cursor: pageParam, limit: pageSize, signal }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.nextCursor,
    enabled: open && Boolean(target) && filter !== null,
  });
  // While the typed text is still waiting for its search, older results are not offered (Enter would pick the wrong one).
  const stale = text !== query;
  const options = stale ? [] : (results.data?.pages.flatMap((page) => page.items) ?? []);
  const canCreate = !stale && Boolean(target && label && target.type !== "view" && query.trim() !== "" && !(target.recordId?.kind === "text" && !target.recordId.autogeneratePattern));
  const full = value.length >= max;

  const select = (record: RecordData) => {
    if (value.includes(record.id)) return;
    queryClient.setQueryData(pickedKey(record.id), record);
    onChange(multiple ? [...value, record.id] : [record.id]);
    setText("");
    setActive(-1);
    if (!multiple) setOpen(false);
  };

  // One create at a time (isPending lags a same-tick double Enter), and one Idempotency-Key per
  // name until the server answers, so a retry after a lost response never creates a twin.
  const creating = useRef(false);
  const createKey = useRef<{ name: string; key: string } | null>(null);
  const create = useMutation({
    mutationFn: (name: string) => {
      if (createKey.current?.name !== name) createKey.current = { name, key: randomKey() };
      return createRecord(target!.name, { [label!.name]: name }, { idempotencyKey: createKey.current.key });
    },
    onSuccess: (result, name) => {
      createKey.current = null;
      select(result.record);
      toast(t("relation.created", { name }));
    },
    onError: (failure) => {
      if (failure instanceof ApiError && failure.status >= 400 && failure.status < 500 && failure.status !== 408 && failure.status !== 429 && !["idempotency_in_progress", "idempotency_conflict"].includes(failure.code)) createKey.current = null;
      toast(errorText(t, failure), "danger");
    },
    onSettled: () => {
      creating.current = false;
    },
  });
  const reportPending = useRef(onPendingChange);
  reportPending.current = onPendingChange;
  useEffect(() => { reportPending.current?.(create.isPending); }, [create.isPending]);
  useEffect(() => () => { reportPending.current?.(false); }, []);
  const startCreate = () => {
    if (creating.current) return;
    creating.current = true;
    create.mutate(query.trim());
  };

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    const count = options.length + (canCreate ? 1 : 0);
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      setOpen(true);
      setActive((index) => (count ? (index + (event.key === "ArrowDown" ? 1 : -1) + count) % count : -1));
    } else if (event.key === "Enter") {
      event.preventDefault();
      if (active >= 0 && active < options.length) select(options[active]);
      else if (active === options.length && canCreate) startCreate();
    } else if (event.key === "Escape" && open) {
      // Closes the list, not the record pane.
      event.preventDefault();
      setOpen(false);
    }
  };

  const listId = `${id}-list`;
  const picked = (selected: string) => lookup.get(selected) ?? queryClient.getQueryData<RecordData>(pickedKey(selected));

  return (
    <div className="field-wrap">
      <div className="field relation" data-invalid={error ? "" : undefined}>
        <label className="field-label" htmlFor={`${id}-search`}>
          {field.name}
          {field.required && <span className="field-required" aria-hidden>*</span>}
          <span className="mono">
            {multiple ? "relation[]" : "relation"} → {target?.name ?? "?"}
          </span>
        </label>
        {value.length > 0 && (
          <ul className="relation-chips" aria-label={t("relation.selected", { field: field.name })}>
            {value.map((selected) => {
              const name = recordLabel(picked(selected), label, selected);
              return (
                <li key={selected} className="relation-chip" title={selected}>
                  <span className={name === selected ? "mono" : undefined}>{name}</span>
                  {target && (
                    <Link to={`/collections/${encodeURIComponent(target.name)}?record=${encodeURIComponent(selected)}`} target="_blank" rel="noopener" className="relation-chip-btn" aria-label={t("relation.open", { name })} title={t("relation.open", { name })}>
                      <RiExternalLinkLine size={13} aria-hidden />
                    </Link>
                  )}
                  {!readOnly && (
                    <button type="button" className="relation-chip-btn" aria-label={t("relation.remove", { name })} title={t("relation.remove", { name })} onClick={() => onChange(value.filter((item) => item !== selected))}>
                      <RiCloseLine size={13} aria-hidden />
                    </button>
                  )}
                </li>
              );
            })}
          </ul>
        )}
        {!readOnly && !full && (
          <div className="relation-search">
            <RiSearchLine size={14} aria-hidden />
            <input
              id={`${id}-search`}
              role="combobox"
              aria-expanded={open}
              aria-controls={listId}
              aria-autocomplete="list"
              aria-activedescendant={active >= 0 ? `${id}-option-${active}` : undefined}
              aria-invalid={error ? true : undefined}
              value={text}
              autoComplete="off"
              spellCheck={false}
              placeholder={label ? t("relation.searchBy", { field: label.name }) : t("relation.searchId")}
              onChange={(event) => {
                setText(event.target.value);
                setOpen(true);
                setActive(-1);
              }}
              onFocus={() => setOpen(true)}
              onBlur={(event) => {
                if (!event.currentTarget.parentElement?.parentElement?.contains(event.relatedTarget)) setOpen(false);
              }}
              onKeyDown={onKeyDown}
            />
          </div>
        )}
        {open && !readOnly && !full && (
          <div className="relation-results">
            <ul id={listId} role="listbox" aria-label={t("relation.results", { field: field.name })}>
              {options.map((record, index) => {
                const chosen = value.includes(record.id);
                return (
                  <li
                    key={record.id}
                    id={`${id}-option-${index}`}
                    role="option"
                    aria-selected={index === active}
                    aria-disabled={chosen || undefined}
                    data-active={index === active ? "" : undefined}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => select(record)}
                  >
                    <span>{recordLabel(record, label, record.id)}</span>
                    <span className="mono txt-hint">{record.id}</span>
                  </li>
                );
              })}
              {canCreate && (
                <li id={`${id}-option-${options.length}`} role="option" aria-selected={active === options.length} data-active={active === options.length ? "" : undefined} aria-disabled={create.isPending || undefined} className="relation-create" onMouseDown={(event) => event.preventDefault()} onClick={startCreate}>
                  <RiAddLine size={14} aria-hidden />
                  <span>{t("relation.create", { name: query.trim(), collection: target!.name })}</span>
                </li>
              )}
            </ul>
            {filter === null ? (
              <p className="relation-note">{t("relation.needId")}</p>
            ) : stale || results.isPending ? (
              <Skeleton variant="list" />
            ) : results.isError ? (
              <p className="relation-note txt-danger">{errorText(t, results.error)}</p>
            ) : options.length === 0 && !canCreate ? (
              <p className="relation-note">{t("relation.none")}</p>
            ) : null}
            {results.hasNextPage && (
              <Button size="sm" variant="transparent" loading={results.isFetchingNextPage} onMouseDown={(event) => event.preventDefault()} onClick={() => results.fetchNextPage()}>
                {t("common.loadMore")}
              </Button>
            )}
          </div>
        )}
      </div>
      {(error || field.help || (multiple && Number.isFinite(max))) && (
        <p className={error ? "field-error" : "field-hint"} role={error ? "alert" : undefined}>
          {error ?? field.help ?? t("relation.limit", { count: value.length, max })}
        </p>
      )}
    </div>
  );
}
