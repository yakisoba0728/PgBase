import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Searchbar } from "../components/Field";
import {
  RiAddLine,
  RiArrowUpDoubleLine,
  RiDeleteBinLine,
  RiDownload2Line,
  RiErrorWarningLine,
  RiFilterLine,
  RiInboxLine,
  RiQuestionLine,
  RiRefreshLine,
} from "@remixicon/react";
import type { Collection } from "../api/collections";
import { ApiError } from "../api/http";
import { deleteRecord, listRecords } from "../api/records";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Dropdown, DropdownItem } from "../components/Popover";
import { EmptyState, Loader, LoadMore } from "../components/Feedback";
import { Popover } from "../components/Popover";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { ValuePeek, type Peek } from "../components/ValuePeek";
import { en } from "../i18n/en";
import { useT, type Translate } from "../i18n";
import { collectAll, downloadJson, exportable, settleLimited } from "./bulk";
import { ColumnSettings } from "./ColumnSettings";
import { useColumnPrefs } from "./columns";
import { collectionsQuery, recordsQuery, removeFromLists } from "./queries";
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
  const queryClient = useQueryClient();
  return (
    <>
      {/* Reset, not invalidate: refetching every loaded page of a long list would cost hundreds of requests. */}
      <IconButton label={t("records.refresh")} icon={RiRefreshLine} onClick={() => queryClient.resetQueries({ queryKey: listKey(collection.name) })} />
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
  const [exporting, setExporting] = useState(false);
  const [peek, setPeek] = useState<Peek | null>(null);
  const [scrolled, setScrolled] = useState(false);
  const filterInput = useRef<HTMLInputElement>(null);
  const scroller = useRef<HTMLDivElement>(null);
  // Follows URL changes from elsewhere (clear, history) without remounting: a remount would drop what is being typed.
  useEffect(() => {
    if (filterInput.current && document.activeElement !== filterInput.current) filterInput.current.value = url.filter;
  }, [url.filter]);
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
    (ids: string[], checked: boolean) =>
      setSelected((previous) => {
        const next = new Set(previous);
        ids.forEach((id) => (checked ? next.add(id) : next.delete(id)));
        return next;
      }),
    [],
  );
  const onPeek = useCallback((label: string, text: string, anchor: HTMLElement) => setPeek({ label, text, anchor }), []);

  const bulkDelete = useMutation({
    mutationFn: async (ids: string[]) => {
      const results = await settleLimited(ids, 6, (id) => deleteRecord(collection.name, id));
      return { ids, failed: ids.filter((_, index) => results[index].status === "rejected"), reason: results.find((result) => result.status === "rejected")?.reason };
    },
    onSuccess: ({ ids, failed, reason }) => {
      const done = ids.filter((id) => !failed.includes(id));
      removeFromLists(queryClient, collection.name, done);
      if (done.length) toast(t("records.deleted", { count: done.length }));
      if (failed.length) toast(t("records.deleteFailed", { count: failed.length, error: errorText(t, reason) }), "danger");
      // Failures stay selected so they can be retried or inspected.
      setSelected(new Set(failed));
      if (url.record && done.includes(url.record)) url.close();
    },
    onSettled: () => setConfirmBulk(false),
  });

  const onFilter = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    url.setFilter(String(new FormData(event.currentTarget).get("filter") ?? ""));
  };

  // Only rows still on screen count, so a filter change cannot widen a delete.
  const chosen = items.filter((record) => selected.has(record.id));
  const count = chosen.length;
  const loaded = records.hasNextPage ? t("records.loadedMore", { count: items.length }) : t("records.count", { count: items.length });
  const problem = records.isError ? queryProblem(t, records.error) : null;

  const exportAll = async () => {
    setExporting(true);
    try {
      const filter = buildFilter(url.filter, collection.fields);
      const { items: all, complete } = await collectAll((cursor) => listRecords(collection.name, { filter: filter || undefined, sort: url.sort || undefined, cursor, limit: 200 }));
      downloadJson(`${collection.name}${complete ? "" : "-partial"}.json`, exportable(all));
      toast(complete ? t("records.exported", { count: all.length }) : t("records.exportedPartial", { count: all.length }), complete ? "info" : "danger");
    } catch (error) {
      toast(errorText(t, error), "danger");
    } finally {
      setExporting(false);
    }
  };

  const toolbar = (
    <div className="toolbar records-toolbar">
      <form role="search" onSubmit={onFilter}>
        <Searchbar
          label={t("records.filter")}
          icon={RiFilterLine}
          mono
          primary
          inputRef={filterInput}
          name="filter"
          defaultValue={url.filter}
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
          <Popover label={t("records.filterHelp")} icon={RiQuestionLine} buttonClassName="icon-btn searchbar-help" className="filter-help">
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
                          if (filterInput.current) {
                            filterInput.current.value = example;
                            filterInput.current.focus();
                          }
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
      {count > 0 && !readOnly ? (
        <div className="toolbar-selection">
          <span>{t("records.selected", { count })}</span>
          <Button size="sm" variant="transparent" onClick={() => setSelected(new Set())}>{t("records.clearSelection")}</Button>
          <Button size="sm" variant="danger" icon={RiDeleteBinLine} onClick={() => setConfirmBulk(true)}>{t("common.delete")}</Button>
        </div>
      ) : (
        <span className="toolbar-count">{records.data ? loaded : ""}</span>
      )}
      <ColumnSettings available={columns.available} prefs={columns.prefs} onChange={columns.save} />
      <Dropdown label={exporting ? t("records.exporting") : t("records.export")} icon={RiDownload2Line} heading={t("records.exportJson")}>
        {count > 0 && <DropdownItem onSelect={() => downloadJson(`${collection.name}-selected.json`, exportable(chosen))}>{t("records.exportSelected", { count })}</DropdownItem>}
        <DropdownItem onSelect={() => downloadJson(`${collection.name}-loaded.json`, exportable(items))}>{t("records.exportLoaded", { count: items.length })}</DropdownItem>
        <DropdownItem onSelect={() => !exporting && void exportAll()}>{t("records.exportAll")}</DropdownItem>
      </Dropdown>
    </div>
  );

  const list = (
    <>
      {toolbar}
      {problem && (
        <p id="records-filter-problem" className="records-problem" role="alert">
          {problem}
          <Button size="sm" variant="transparent" onClick={() => url.setFilter("")}>{t("records.clearFilter")}</Button>
        </p>
      )}
      <div className="records-body">
      <div className="grid-scroll records-scroll" ref={scroller}>
        {records.isPending ? (
          <Loader />
        ) : records.isError && !records.data ? (
          problem ? (
            <EmptyState icon={RiFilterLine}>{t("records.fixFilter")}</EmptyState>
          ) : (
            <EmptyState icon={RiErrorWarningLine} action={<Button size="sm" variant="secondary" icon={RiRefreshLine} onClick={() => records.refetch()}>{t("common.retry")}</Button>}>
              {errorText(t, records.error)}
            </EmptyState>
          )
        ) : items.length === 0 ? (
          <EmptyState
            icon={RiInboxLine}
            action={
              url.filter ? (
                <Button size="sm" variant="secondary" onClick={() => url.setFilter("")}>{t("records.clearFilter")}</Button>
              ) : (
                !readOnly && <Button size="sm" icon={RiAddLine} onClick={url.create}>{t("records.new")}</Button>
              )
            }
          >
            {url.filter ? t("records.noMatch") : t("records.empty")}
          </EmptyState>
        ) : (
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
      </div>
        {scrolled && (
          <button type="button" className="to-top" onClick={() => scroller.current?.scrollTo({ top: 0 })} aria-label={t("records.toTop")} title={t("records.toTop")}>
            <RiArrowUpDoubleLine size={16} aria-hidden />
          </button>
        )}
      </div>
      <ValuePeek peek={peek} onClose={() => setPeek(null)} />
      <ConfirmDialog
        open={confirmBulk}
        title={t("records.deleteManyTitle", { count })}
        message={t("records.deleteManyText")}
        confirmLabel={t("common.delete")}
        danger
        busy={bulkDelete.isPending}
        onConfirm={() => bulkDelete.mutate(chosen.map((record) => record.id))}
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
