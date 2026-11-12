import { useState, type FormEvent } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { RiDeleteBinLine, RiErrorWarningLine, RiFileList3Line, RiRefreshLine, RiRouteLine } from "@remixicon/react";
import { clearRequestLogs } from "../api/logs";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Loader } from "../components/Feedback";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { statusTone } from "./format";
import { LogDetail } from "./LogDetail";
import { LogStats } from "./LogStats";
import { logFilterFrom, logsKey, logsQuery, useTimeFormat } from "./queries";
import { useSearchState } from "../app/search";
import "../styles/ops.css";

const methods = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"];
const filterKeys = ["status", "method", "route", "from", "to"];

/** Header actions: refresh and delete-all. */
export function LogsActions() {
  const t = useT();
  const { update } = useSearchState();
  const queryClient = useQueryClient();
  const [confirm, setConfirm] = useState(false);
  const clear = useMutation({
    mutationFn: clearRequestLogs,
    onSuccess: () => {
      // Ids restart at 1, so nothing cached may survive.
      update({ entry: null });
      void queryClient.resetQueries({ queryKey: logsKey });
      toast(t("ops.logs.cleared"));
    },
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => setConfirm(false),
  });
  return (
    <>
      <IconButton label={t("ops.refresh")} icon={RiRefreshLine} onClick={() => queryClient.invalidateQueries({ queryKey: logsKey })} />
      <Button size="sm" variant="secondary" icon={RiDeleteBinLine} onClick={() => setConfirm(true)}>{t("ops.logs.clear")}</Button>
      <ConfirmDialog
        open={confirm}
        title={t("ops.logs.clearTitle")}
        message={t("ops.logs.clearText")}
        confirmLabel={t("ops.logs.clearConfirm")}
        danger
        busy={clear.isPending}
        onConfirm={() => clear.mutate()}
        onCancel={() => setConfirm(false)}
      />
    </>
  );
}

/** Request logs: status-class bars, filters, the newest-first list with "load more", and the entry pane. */
export function LogsView() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useSearchState();
  const filter = logFilterFrom(params);
  const logs = useInfiniteQuery(logsQuery(filter));
  const items = logs.data?.pages.flatMap((page) => page.items) ?? [];
  const entry = params.get("entry");
  const filtered = filterKeys.some((key) => params.has(key));
  const set = (key: string) => (value: string) => update({ [key]: value || null });

  const onRoute = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    update({ route: String(new FormData(event.currentTarget).get("route") ?? "").trim() || null });
  };

  const list = (
    <>
      <LogStats active={params.get("status") ?? ""} onPick={set("status")} />
      <div className="toolbar ops-filters">
        <select className="ops-control" aria-label={t("ops.logs.status")} value={params.get("status") ?? ""} onChange={(event) => set("status")(event.target.value)}>
          <option value="">{t("ops.logs.anyStatus")}</option>
          {[1, 2, 3, 4, 5].map((c) => (
            <option key={c} value={c}>{c}xx</option>
          ))}
        </select>
        <select className="ops-control" aria-label={t("ops.logs.method")} value={params.get("method") ?? ""} onChange={(event) => set("method")(event.target.value)}>
          <option value="">{t("ops.logs.anyMethod")}</option>
          {methods.map((method) => (
            <option key={method} value={method}>{method}</option>
          ))}
        </select>
        <form role="search" onSubmit={onRoute}>
          <div className="searchbar mono">
            <RiRouteLine size={14} aria-hidden />
            <input key={params.get("route") ?? ""} name="route" type="search" defaultValue={params.get("route") ?? ""} aria-label={t("ops.logs.route")} placeholder={t("ops.logs.routePlaceholder")} spellCheck={false} autoComplete="off" />
          </div>
        </form>
        <label className="ops-range">
          <span>{t("ops.logs.from")}</span>
          <input className="ops-control" type="datetime-local" step={1} value={params.get("from") ?? ""} onChange={(event) => set("from")(event.target.value)} />
        </label>
        <label className="ops-range">
          <span>{t("ops.logs.to")}</span>
          <input className="ops-control" type="datetime-local" step={1} value={params.get("to") ?? ""} onChange={(event) => set("to")(event.target.value)} />
        </label>
        {filtered && <Button size="sm" variant="transparent" onClick={() => update(Object.fromEntries(filterKeys.map((key) => [key, null])))}>{t("ops.clearFilters")}</Button>}
        {logs.data && <span className="toolbar-count">{logs.hasNextPage ? t("ops.logs.loadedMore", { count: items.length }) : t("ops.logs.count", { count: items.length })}</span>}
      </div>
      <div className="grid-scroll">
        {logs.isPending ? (
          <Loader />
        ) : logs.isError ? (
          <EmptyState icon={RiErrorWarningLine}>{errorText(t, logs.error)}</EmptyState>
        ) : items.length === 0 ? (
          <EmptyState icon={RiFileList3Line}>{filtered ? t("ops.logs.noMatch") : t("ops.logs.empty")}</EmptyState>
        ) : (
          <>
            <table className="grid ops-grid" aria-label={t("nav.logs")}>
              <thead>
                <tr>
                  <th>{t("ops.logs.time")}</th>
                  <th>{t("ops.logs.status")}</th>
                  <th>{t("ops.logs.request")}</th>
                  <th className="num">{t("ops.logs.duration")}</th>
                  <th>{t("ops.logs.attributes")}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => {
                  const id = String(item.id);
                  return (
                    <tr
                      key={id}
                      tabIndex={0}
                      aria-current={entry === id ? "true" : undefined}
                      onClick={() => update({ entry: id })}
                      onKeyDown={(event) => {
                        if (event.key === "Enter") update({ entry: id });
                      }}
                    >
                      <td className="mono ops-time">{time(item.at)}</td>
                      <td><span className="chip mono" data-tone={statusTone(item.status)}>{item.status}</span></td>
                      <td className="ops-request">
                        <span className="ops-method">{item.method}</span>
                        <span className="mono">{item.route}</span>
                      </td>
                      <td className="num">{String(item.durationMs)} ms</td>
                      <td>
                        {item.clientIP && <span className="chip mono">{item.clientIP}</span>}
                        {item.authKind && <span className="chip">{item.authKind}</span>}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {logs.hasNextPage && (
              <div className="grid-foot">
                <Button size="sm" variant="secondary" loading={logs.isFetchingNextPage} onClick={() => logs.fetchNextPage()}>{t("records.loadMore")}</Button>
              </div>
            )}
          </>
        )}
      </div>
    </>
  );

  return (
    <SplitView
      list={list}
      paneLabel={t("ops.logs.detail")}
      onClose={() => update({ entry: null })}
      pane={entry ? <LogDetail key={entry} id={entry} onClose={() => update({ entry: null })} onFilterRoute={(method, route) => update({ method, route })} /> : null}
    />
  );
}
