import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Searchbar } from "../components/Field";
import {
  RiAddLine,
  RiArrowUpDoubleLine,
  RiCloseLine,
  RiDeleteBinLine,
  RiDownload2Line,
  RiFilterLine,
  RiInboxLine,
  RiQuestionLine,
} from "@remixicon/react";
import type { Collection } from "../api/collections";
import { ApiError } from "../api/http";
import { deleteRecord, deleteRecordsBatch, listRecords, type RecordData } from "../api/records";
import { errorText } from "../app/queries";
import { Button, IconButton, RefreshButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Dropdown, DropdownItem } from "../components/Popover";
import { EmptyState, LoadMore } from "../components/Feedback";
import { JobProgress } from "../components/JobProgress";
import { QueryView } from "../components/QueryView";
import { TableScroll } from "../components/Table";
import { Popover } from "../components/Popover";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { ValuePeek, type Peek } from "../components/ValuePeek";
import { en } from "../i18n/en";
import { useT, type Translate } from "../i18n";
import { settingsQuery } from "../system/queries";
import { collectAll, deleteAll, exportable } from "./bulk";
import { downloadJson } from "../app/download";
import { ColumnSettings } from "./ColumnSettings";
import { useColumnPrefs } from "./columns";
import { collectionsQuery, countText, recordsQuery, removeFromLists } from "./queries";
import { RecordEditor } from "./RecordEditor";
import { RecordsTable } from "./RecordsTable";
import { expandFor } from "./relation";
import { buildFilter, useRecordsUrl } from "./useRecordsUrl";
import "../styles/records.css";

const listKey = (collection: string) => ["records", collection, "list"];

/** Header actions of the Records tab. */
export function RecordsActions({ collection }: { collection: Collection }) {
  const t = useT();
  const url = useRecordsUrl();
  return (
    <>
      {/* Reset, not invalidate: refetching every loaded page of a long list would cost hundreds of requests. */}
      <RefreshButton queryKey={listKey(collection.name)} reset />
      {collection.type !== "view" && (
        <Button size="sm" icon={RiAddLine} data-new onClick={url.create}>{t("records.new")}</Button>
      )}
    </>
  );
}

/** Text for a rejected filter or sort, from the server's validation code. */
function queryProblem(t: Translate, error: unknown): string | null {
  if (!(error instanceof ApiError) || error.code !== "validation_failed" || !error.details || typeof error.details !== "object") return null;
  const details = error.details as Record<string, string[] | undefined>;
  const part = details.filter ? "filter" : details.sort ? "sort" : null;
  if (!part) return null;
  const code = details[part]![0];
  const key = `query.${code}`;
  return `${t(part === "filter" ? "query.filterRejected" : "query.sortRejected")} ${key in en ? t(key as keyof typeof en) : code}`;
}

/** A bulk delete or export in progress, or its outcome when it needs attention (a clean finish is a toast). */
interface BulkTask {
  kind: "delete" | "export";
  state: "running" | "failed" | "attention";
  detail: string;
  done?: number;
  total?: number;
  error?: string;
  /** Delete: the ids that failed, for "Retry failed". */
  failed?: string[];
  /** Export: the rows fetched before it stopped. */
  rows?: RecordData[];
  abort: AbortController;
}

// Both walks stop here: 100 pages of 200 rows.
const maxPages = 100;

const examples = ["title ~ 'draft'", "published = true && score >= 10", "created_at > '2026-01-01T00:00:00Z'", "name:lower = 'ada'", "author = '<id>'"];

/** The Records tab: filterable, sortable, windowed grid with cursor paging, bulk actions and the split detail pane. */
export function RecordsView({ collection }: { collection: Collection }) {
  const t = useT();
  const url = useRecordsUrl();
  const queryClient = useQueryClient();
  const catalog = useQuery(collectionsQuery).data ?? [];
  const columns = useColumnPrefs(collection);
  const expand = expandFor(collection, columns.visible);
  const records = useInfiniteQuery(recordsQuery(collection.name, buildFilter(url.filter, collection.fields), url.sort, expand));
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmBulk, setConfirmBulk] = useState(false);
  const [task, setTask] = useState<BulkTask | null>(null);
  const running = task?.state === "running";
  // Leaving the screen stops a bulk task from issuing further requests.
  const taskAbort = useRef<AbortController | null>(null);
  const left = useRef(false);
  const openRecord = useRef(url.record);
  openRecord.current = url.record;
  useEffect(() => {
    left.current = false;
    return () => {
      left.current = true;
      taskAbort.current?.abort();
    };
  }, []);
  // "All matching" is pinned to the filter it was chosen under, so a filter change can never widen it.
  const scopeFilter = buildFilter(url.filter, collection.fields);
  const [matchScope, setMatchScope] = useState<string | null>(null);
  const [peek, setPeek] = useState<Peek | null>(null);
  const [scrolled, setScrolled] = useState(false);
  const filterInput = useRef<HTMLInputElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // Typed text is applied on submit; it follows URL changes from elsewhere (clear, history).
  const [text, setText] = useState(url.filter);
  useEffect(() => setText(url.filter), [url.filter]);
  const items = records.data?.pages.flatMap((page) => page.items) ?? [];
  const readOnly = collection.type === "view";

  // Loads the next page when the user scrolls near the end; the button stays for keyboard users.
  const { hasNextPage, isFetchingNextPage, isFetchNextPageError, fetchNextPage } = records;
  useEffect(() => {
    const element = scroller.current;
    if (!element) return;
    const onScroll = () => {
      setScrolled(element.scrollTop > 1200);
      if (hasNextPage && !isFetchingNextPage && !isFetchNextPageError && element.scrollHeight - element.scrollTop - element.clientHeight < 800) void fetchNextPage();
    };
    element.addEventListener("scroll", onScroll, { passive: true });
    return () => element.removeEventListener("scroll", onScroll);
  }, [hasNextPage, isFetchingNextPage, isFetchNextPageError, fetchNextPage]);

  const select = useCallback(
    (ids: string[], checked: boolean) => {
      setMatchScope(null);
      setSelected((previous) => {
        const next = new Set(previous);
        ids.forEach((id) => (checked ? next.add(id) : next.delete(id)));
        return next;
      });
    },
    [],
  );
  const onPeek = useCallback((label: string, text: string, anchor: HTMLElement) => setPeek({ label, text, anchor }), []);

  const start = (kind: BulkTask["kind"], detail: string) => {
    const abort = new AbortController();
    taskAbort.current = abort;
    setTask({ kind, state: "running", detail, abort });
    return abort;
  };

  /** Deletes the given ids, or every record matching `filter` (ids collected first: cursors expire, and deleting while paging would skip rows). */
  const runDelete = async (source: string[] | { filter: string }) => {
    const abort = start("delete", t("records.deleteStarting"));
    const progress = (patch: Partial<BulkTask>) => setTask((current) => (current?.abort === abort ? { ...current, ...patch } : current));
    let ids: string[];
    let capped = false;
    try {
      if (Array.isArray(source)) ids = source;
      else {
        const found = await collectAll((cursor) => listRecords(collection.name, { filter: source.filter || undefined, fields: "id", cursor, limit: 200, signal: abort.signal }), maxPages, {
          signal: abort.signal,
          onPage: (rows) => progress({ detail: t("records.deleteFinding", { count: rows.length }) }),
        });
        ids = found.items.map((record) => record.id);
        capped = !found.complete;
      }
    } catch (error) {
      setTask(abort.signal.aborted ? null : { kind: "delete", state: "failed", detail: t("records.deleteNotStarted"), error: errorText(t, error), abort });
      return;
    }
    const batchSize = await queryClient.fetchQuery(settingsQuery).then((settings) => (settings.batch.enabled ? settings.batch.maxRequests : 0), () => 0);
    const total = ids.length;
    progress({ detail: t("records.deleteProgress", { done: 0, total }), done: 0, total });
    const outcome = await deleteAll(ids, {
      batchSize,
      batch: (chunk) => deleteRecordsBatch(collection.name, chunk),
      one: (id) => deleteRecord(collection.name, id),
      signal: abort.signal,
      onProgress: (done, failed) => progress({ done, detail: failed ? t("records.deleteProgressFailed", { done, total, failed }) : t("records.deleteProgress", { done, total }) }),
    });
    // One list update for the whole run.
    if (Array.isArray(source)) removeFromLists(queryClient, collection.name, outcome.deleted);
    else void queryClient.resetQueries({ queryKey: listKey(collection.name) });
    // Failures stay selected so they can be retried or inspected; a selection made during the run is kept.
    setSelected((previous) => {
      const next = new Set([...previous, ...outcome.failed]);
      outcome.deleted.forEach((id) => next.delete(id));
      return next;
    });
    setMatchScope(null);
    // The record open now, not at the start: another may have been opened meanwhile, or the screen left.
    const open = openRecord.current;
    if (!left.current && open && outcome.deleted.includes(open)) url.close();
    if (outcome.deleted.length) toast(t("records.deleted", { count: outcome.deleted.length }));
    const { failed, stopped } = outcome;
    if (!failed.length && !stopped && !capped) {
      setTask(null);
      return;
    }
    const detail = [
      failed.length && t("records.deletePartial", { done: outcome.deleted.length, failed: failed.length }),
      stopped && t("records.deleteStopped", { done: outcome.deleted.length, rest: total - outcome.deleted.length - failed.length }),
      capped && t("records.deleteCapped", { count: total }),
    ].filter(Boolean).join(" ");
    // Left the screen mid-run: the progress panel is gone, so the outcome must not read as a clean finish.
    if (left.current) toast(detail, "danger");
    setTask({ kind: "delete", state: failed.length ? "failed" : "attention", detail, done: outcome.deleted.length, total, error: failed.length ? errorText(t, outcome.reason) : undefined, failed, abort });
  };

  const onFilter = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    url.setFilter(text);
  };

  // Only rows still on screen count, so a filter change cannot widen a delete.
  const chosen = items.filter((record) => selected.has(record.id));
  const count = chosen.length;
  const matchingAll = !readOnly && matchScope === scopeFilter;
  const first = records.data?.pages[0];
  const total = first?.totalItems === undefined ? undefined : `${first.totalItems.toLocaleString()}${first.totalCapped ? "+" : ""}`;
  const loaded = countText(t, items.length, records.hasNextPage, records.data?.pages[0]);
  const problem = records.isError ? queryProblem(t, records.error) : null;

  const runExport = async () => {
    const abort = start("export", t("records.exportProgress", { count: 0 }));
    let rows: RecordData[] = [];
    try {
      const { items: all, complete } = await collectAll(
        (cursor) => listRecords(collection.name, { filter: scopeFilter || undefined, sort: url.sort || undefined, cursor, limit: 200, total: cursor ? undefined : true, signal: abort.signal }),
        maxPages,
        {
          signal: abort.signal,
          onPage: (got, page) => {
            rows = got;
            setTask((current) => {
              if (current?.abort !== abort) return current;
              // A capped count is only a lower bound: no invented percentage.
              const known = current.total ?? (page.totalItems !== undefined && !page.totalCapped ? page.totalItems : undefined);
              return { ...current, done: got.length, total: known, detail: t("records.exportProgress", { count: got.length }) };
            });
          },
        },
      );
      downloadJson(`${collection.name}${complete ? "" : "-partial"}.json`, exportable(all));
      if (complete) {
        setTask(null);
        toast(t("records.exported", { count: all.length }));
      } else setTask({ kind: "export", state: "attention", detail: t("records.exportedPartial", { count: all.length }), abort });
    } catch (error) {
      setTask(abort.signal.aborted ? null : { kind: "export", state: "failed", detail: t("records.exportStopped", { count: rows.length }), error: errorText(t, error), rows, abort });
    }
  };

  const taskView = task && (
    <JobProgress
      title={t(task.kind === "delete" ? "records.deleteTask" : "records.exportTask")}
      state={task.state}
      detail={task.detail}
      done={task.done}
      total={task.total}
      error={task.error}
      action={
        <div className="records-task-actions">
          {running ? (
            <Button size="sm" variant="secondary" onClick={() => {
              task.abort.abort();
              // An export is only reads: nothing to wait for.
              if (task.kind === "export") setTask(null);
            }}>{t("records.stop")}</Button>
          ) : (
            <>
              {task.failed && task.failed.length > 0 && <Button size="sm" variant="secondary" onClick={() => void runDelete(task.failed!)}>{t("records.retryFailed", { count: task.failed.length })}</Button>}
              {task.kind === "export" && task.state === "failed" && <Button size="sm" variant="secondary" onClick={() => void runExport()}>{t("common.retry")}</Button>}
              {task.rows && task.rows.length > 0 && <Button size="sm" variant="transparent" onClick={() => downloadJson(`${collection.name}-partial.json`, exportable(task.rows!))}>{t("records.savePartial", { count: task.rows.length })}</Button>}
              <IconButton label={t("common.dismiss")} icon={RiCloseLine} onClick={() => setTask(null)} />
            </>
          )}
        </div>
      }
    />
  );

  const toolbar = (
    <div className="toolbar records-toolbar">
      <form role="search" onSubmit={onFilter}>
        <Searchbar size="sm"
          label={t("records.filter")}
          icon={RiFilterLine}
          mono
          primary
          inputRef={filterInput}
          value={text}
          onChange={(event) => {
            setText(event.target.value);
            if (!event.target.value) url.setFilter("", true);
          }}
          invalid={Boolean(problem)}
          aria-describedby={problem ? "records-filter-problem" : undefined}
          placeholder={t("records.filterPlaceholder")}
          onKeyDown={(event) => {
            if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
              event.preventDefault();
              event.currentTarget.form?.requestSubmit();
            }
          }}
        >
          <Popover label={t("records.filterHelp")} icon={RiQuestionLine} buttonClassName="icon-btn icon-btn-xs" className="filter-help">
            {() => (
              <>
                <p className="filter-help-intro">{t("records.filterHelpText")}</p>
                <ul>
                  {examples.map((example) => (
                    <li key={example}>
                      <button
                        type="button"
                        className="mono"
                        onClick={() => {
                          setText(example);
                          filterInput.current?.focus();
                        }}
                      >
                        {example}
                      </button>
                    </li>
                  ))}
                </ul>
                <p className="txt-hint">{t("records.filterOps")}</p>
              </>
            )}
          </Popover>
        </Searchbar>
      </form>
      {(count > 0 || matchingAll) && !readOnly ? (
        <div className="toolbar-selection">
          <span>{matchingAll ? (total ? t("records.matchingSelected", { total }) : t("records.matchingSelectedUnknown")) : t("records.selected", { count })}</span>
          {!matchingAll && count === items.length && records.hasNextPage && (
            <Button size="sm" variant="transparent" onClick={() => setMatchScope(scopeFilter)}>{total ? t("records.selectMatching", { total }) : t("records.selectMatchingUnknown")}</Button>
          )}
          <Button size="sm" variant="transparent" onClick={() => {
            setSelected(new Set());
            setMatchScope(null);
          }}>{t("records.clearSelection")}</Button>
          <Button size="sm" variant="danger" icon={RiDeleteBinLine} disabled={running} onClick={() => setConfirmBulk(true)}>{t("common.delete")}</Button>
        </div>
      ) : (
        <span className="toolbar-count">{records.data ? loaded : ""}</span>
      )}
      <ColumnSettings available={columns.available} prefs={columns.prefs} onChange={columns.save} />
      <Dropdown label={running && task?.kind === "export" ? t("records.exporting") : t("records.export")} icon={RiDownload2Line} heading={t("records.exportJson")}>
        {/* With every matching record selected, "All matching records" below is the selection. */}
        {count > 0 && !matchingAll && <DropdownItem onSelect={() => downloadJson(`${collection.name}-selected.json`, exportable(chosen))}>{t("records.exportSelected", { count })}</DropdownItem>}
        <DropdownItem onSelect={() => downloadJson(`${collection.name}-loaded.json`, exportable(items))}>{t("records.exportLoaded", { count: items.length })}</DropdownItem>
        <DropdownItem onSelect={() => !running && void runExport()}>{t("records.exportAll")}</DropdownItem>
      </Dropdown>
    </div>
  );

  const list = (
    <>
      {toolbar}
      {problem && (
        <p id="records-filter-problem" className="records-problem" role="alert">
          {problem}
          <Button size="sm" variant="transparent" onClick={url.clearQuery}>{t("records.clearFilter")}</Button>
        </p>
      )}
      {taskView}
      <div className="records-body">
      <TableScroll className="records-scroll" ref={scroller}>
        {problem && !records.data ? (
          <EmptyState icon={RiFilterLine}>{t("records.fixFilter")}</EmptyState>
        ) : (
          <QueryView
            query={records}
            isEmpty={() => items.length === 0}
            empty={
              <EmptyState
                icon={RiInboxLine}
                // The header's "New record" stays the one create action (and the "n" shortcut).
                action={url.filter && <Button size="sm" variant="secondary" onClick={() => url.setFilter("")}>{t("records.clearFilter")}</Button>}
              >
                {url.filter ? t("records.noMatch") : t("records.empty")}
              </EmptyState>
            }
          >
            {() => (
              <>
                <RecordsTable
                  collection={collection}
                  catalog={catalog}
                  columns={columns.visible}
                  records={items}
                  sort={url.sort}
                  onSort={url.toggleSort}
                  openId={url.record}
                  onOpen={url.open}
                  selected={selected}
                  onSelect={select}
                  scroller={scroller}
                  onPeek={onPeek}
                />
                <LoadMore query={records} />
              </>
            )}
          </QueryView>
        )}
      </TableScroll>
        {scrolled && (
          <IconButton className="icon-btn-raised to-top" label={t("records.toTop")} icon={RiArrowUpDoubleLine} onClick={() => scroller.current?.scrollTo({ top: 0 })} />
        )}
      </div>
      <ValuePeek peek={peek} onClose={() => setPeek(null)} />
      <ConfirmDialog
        open={confirmBulk}
        title={matchingAll ? (total ? t("records.deleteMatchingTitle", { total }) : t("records.deleteMatchingTitleUnknown")) : t("records.deleteManyTitle", { count })}
        message={matchingAll ? (scopeFilter ? t("records.deleteMatchingText", { filter: url.filter }) : t("records.deleteEverythingText")) : t("records.deleteManyText")}
        confirmLabel={t("common.delete")}
        danger
        typed={{ phrase: collection.name, label: t("common.typeToConfirm", { name: collection.name }) }}
        onConfirm={() => {
          setConfirmBulk(false);
          void runDelete(matchingAll ? { filter: scopeFilter } : chosen.map((record) => record.id));
        }}
        onCancel={() => setConfirmBulk(false)}
      />
    </>
  );

  const paneOpen = url.record !== null || (url.creating && !readOnly);
  return (
    <SplitView
      list={list}
      paneLabel={t("records.detail")}
      onClose={url.close}
      pane={paneOpen ? <RecordEditor key={url.record ?? "new"} collection={collection} recordId={url.record} onClose={url.close} onCreated={url.open} onCreate={url.create} /> : null}
    />
  );
}
