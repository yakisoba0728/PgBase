import { useState, type FormEvent } from "react";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Searchbar } from "../components/Field";
import { openRow } from "../components/DataGrid";
import { RiDeleteBinLine, RiFileList3Line, RiRefreshLine, RiRouteLine } from "@remixicon/react";
import { clearRequestLogs } from "../api/logs";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, ErrorState, Loader, LoadMore } from "../components/Feedback";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { statusTone } from "./format";
import { LogDetail } from "./LogDetail";
import { LogStats } from "./LogStats";
import { logFilterFrom, logsKey, logsQuery, logStatsQuery } from "./queries";
import { useTimeFormat } from "../app/format";
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
  const stats = useQuery({ ...logStatsQuery, enabled: confirm });
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
      {/* Back to the newest page; refetching every loaded page would cost one request each. */}
      <IconButton label={t("ops.refresh")} icon={RiRefreshLine} onClick={() => queryClient.resetQueries({ queryKey: logsKey })} />
      <Button size="sm" variant="secondary" icon={RiDeleteBinLine} onClick={() => setConfirm(true)}>{t("ops.logs.clear")}</Button>
      <ConfirmDialog
        open={confirm}
        title={t("ops.logs.clearTitle")}
        message={stats.data ? t("ops.logs.clearTextCount", { count: String(stats.data.total) }) : t("ops.logs.clearText")}
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
  const queryClient = useQueryClient();
  const items = logs.data?.pages.flatMap((page) => page.items) ?? [];
  // The window moved past the newest entries (see logWindowPages).
  const shifted = logs.data !== undefined && logs.data.pageParams[0] !== undefined;
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
          <Searchbar key={params.get("route") ?? ""} label={t("ops.logs.route")} icon={RiRouteLine} mono primary name="route" defaultValue={params.get("route") ?? ""} placeholder={t("ops.logs.routePlaceholder")} />
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
        {logs.data && <span className="toolbar-count">{logs.hasNextPage || shifted ? t("ops.logs.window", { count: items.length }) : t("ops.logs.count", { count: items.length })}</span>}
      </div>
      <div className="grid-scroll">
        {logs.isPending ? (
          <Loader />
        ) : logs.isError ? (
          <ErrorState error={logs.error} onRetry={logs.refetch} />
        ) : items.length === 0 ? (
          <EmptyState icon={RiFileList3Line}>{filtered ? t("ops.logs.noMatch") : t("ops.logs.empty")}</EmptyState>
        ) : (
          <>
            {shifted && (
              <div className="ops-window">
                <span>{t("ops.logs.shifted", { count: items.length })}</span>
                <Button size="sm" variant="secondary" onClick={(event) => {
                  event.currentTarget.closest(".grid-scroll")?.scrollTo(0, 0);
                  void queryClient.resetQueries({ queryKey: logsQuery(filter).queryKey });
                }}>{t("ops.logs.newest")}</Button>
              </div>
            )}
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
                      {...openRow(() => update({ entry: id }), entry === id)}
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
            <LoadMore query={logs} auto />
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
