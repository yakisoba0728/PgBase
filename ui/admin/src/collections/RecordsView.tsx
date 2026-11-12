import { useEffect, useRef, useState, type FormEvent } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiDeleteBinLine, RiErrorWarningLine, RiFilterLine, RiInboxLine, RiRefreshLine } from "@remixicon/react";
import type { Collection } from "../api/collections";
import { deleteRecord } from "../api/records";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Loader } from "../components/Feedback";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { recordsQuery } from "./queries";
import { RecordEditor } from "./RecordEditor";
import { RecordsTable } from "./RecordsTable";
import { buildFilter, useRecordsUrl } from "./useRecordsUrl";

/** Header actions of the Records tab. */
export function RecordsActions({ collection }: { collection: Collection }) {
  const t = useT();
  const url = useRecordsUrl();
  const queryClient = useQueryClient();
  return (
    <>
      <IconButton label={t("records.refresh")} icon={RiRefreshLine} onClick={() => queryClient.invalidateQueries({ queryKey: ["records", collection.name, "list"] })} />
      {collection.type !== "view" && (
        <Button size="sm" icon={RiAddLine} onClick={url.create}>{t("records.new")}</Button>
      )}
    </>
  );
}

/** The Records tab: filterable, sortable grid with cursor paging, bulk delete and the split detail pane. */
export function RecordsView({ collection }: { collection: Collection }) {
  const t = useT();
  const url = useRecordsUrl();
  const queryClient = useQueryClient();
  const records = useInfiniteQuery(recordsQuery(collection.name, buildFilter(url.filter, collection.fields), url.sort));
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [confirmBulk, setConfirmBulk] = useState(false);
  const filterInput = useRef<HTMLInputElement>(null);
  // Follows URL changes from elsewhere (clear, history) without remounting: a remount would drop what is being typed.
  useEffect(() => {
    if (filterInput.current && document.activeElement !== filterInput.current) filterInput.current.value = url.filter;
  }, [url.filter]);
  const items = records.data?.pages.flatMap((page) => page.items) ?? [];
  const readOnly = collection.type === "view";

  const select = (ids: string[], checked: boolean) =>
    setSelected((previous) => {
      const next = new Set(previous);
      ids.forEach((id) => (checked ? next.add(id) : next.delete(id)));
      return next;
    });

  const bulkDelete = useMutation({
    mutationFn: async (ids: string[]) => {
      const results = await Promise.allSettled(ids.map((id) => deleteRecord(collection.name, id)));
      return { ids, failed: results.filter((result) => result.status === "rejected") as PromiseRejectedResult[] };
    },
    onSuccess: ({ ids, failed }) => {
      const done = ids.length - failed.length;
      if (done) toast(t("records.deleted", { count: done }));
      if (failed.length) toast(t("records.deleteFailed", { count: failed.length, error: errorText(t, failed[0].reason) }), "danger");
      setSelected(new Set());
      if (url.record && ids.includes(url.record)) url.close();
    },
    onSettled: () => {
      setConfirmBulk(false);
      return queryClient.invalidateQueries({ queryKey: ["records", collection.name, "list"] });
    },
  });

  const onFilter = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    url.setFilter(String(new FormData(event.currentTarget).get("filter") ?? ""));
  };

  // Only rows still on screen count, so a filter change cannot widen a delete.
  const chosen = items.filter((record) => selected.has(record.id)).map((record) => record.id);
  const count = chosen.length;
  const loaded = records.hasNextPage ? t("records.loadedMore", { count: items.length }) : t("records.count", { count: items.length });

  const list = (
    <>
      <div className="toolbar">
        <form role="search" onSubmit={onFilter}>
          <div className="searchbar mono">
            <RiFilterLine size={14} aria-hidden />
            <input ref={filterInput} name="filter" type="search" defaultValue={url.filter} aria-label={t("records.filter")} placeholder={t("records.filterPlaceholder")} spellCheck={false} autoComplete="off" />
          </div>
        </form>
        {count > 0 && !readOnly ? (
          <div className="toolbar-selection">
            <span>{t("records.selected", { count })}</span>
            <Button size="sm" variant="transparent" onClick={() => setSelected(new Set())}>{t("records.clearSelection")}</Button>
            <Button size="sm" variant="danger" icon={RiDeleteBinLine} onClick={() => setConfirmBulk(true)}>{t("common.delete")}</Button>
          </div>
        ) : (
          records.data && <span className="toolbar-count">{loaded}</span>
        )}
      </div>
      <div className="grid-scroll">
        {records.isPending ? (
          <Loader />
        ) : records.isError ? (
          <EmptyState icon={RiErrorWarningLine}>{errorText(t, records.error)}</EmptyState>
        ) : items.length === 0 ? (
          <EmptyState icon={RiInboxLine} action={url.filter && <Button size="sm" variant="secondary" onClick={() => url.setFilter("")}>{t("records.clearFilter")}</Button>}>
            {url.filter ? t("records.noMatch") : t("records.empty")}
          </EmptyState>
        ) : (
          <>
            <RecordsTable
              label={collection.name}
              fields={collection.fields}
              records={items}
              sort={url.sort}
              onSort={url.toggleSort}
              openId={url.record}
              onOpen={url.open}
              selected={selected}
              onSelect={select}
              sqlView={Boolean(collection.view?.sql)}
            />
            {records.hasNextPage && (
              <div className="grid-foot">
                <Button size="sm" variant="secondary" loading={records.isFetchingNextPage} onClick={() => records.fetchNextPage()}>{t("records.loadMore")}</Button>
              </div>
            )}
          </>
        )}
      </div>
      <ConfirmDialog
        open={confirmBulk}
        title={t("records.deleteManyTitle", { count })}
        message={t("records.deleteManyText")}
        confirmLabel={t("common.delete")}
        danger
        busy={bulkDelete.isPending}
        onConfirm={() => bulkDelete.mutate(chosen)}
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
      pane={paneOpen ? <RecordEditor key={url.record ?? "new"} collection={collection} recordId={url.record} onClose={url.close} onCreated={url.open} /> : null}
    />
  );
}
