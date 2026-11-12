import type { ReactNode } from "react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Facts } from "../components/Facts";
import { openRow } from "../components/DataGrid";
import { RiHistoryLine, RiRefreshLine } from "@remixicon/react";
import { stringify } from "lossless-json";
import type { Collection } from "../api/collections";
import type { OperationReceipt, OperationStatus } from "../api/schema";
import { IconButton } from "../components/Button";
import { CodeBlock } from "../components/CodeBlock";
import { EmptyState, ErrorState, Loader, LoadMore } from "../components/Feedback";
import { SplitView, PaneHeader } from "../components/SplitView";
import { collectionsQuery } from "../collections/queries";
import { useT } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { schemaOperationQuery, schemaOperationsQuery } from "./queries";
import "../styles/ops.css";
import "../styles/devtools.css";

const tone: Record<OperationStatus, string | undefined> = { running: "accent", unknown: "warning", failed: "danger", committed: undefined };

function StatusChip({ status }: { status: OperationStatus }) {
  const t = useT();
  return (
    <span className="chip" data-tone={tone[status]}>
      {(status === "running" || status === "unknown") && <span className="loader" aria-hidden />}
      {t(`devtools.ops.status.${status}`)}
    </span>
  );
}

function Target({ id, catalog }: { id: string; catalog: Collection[] | undefined }) {
  const name = catalog?.find((collection) => collection.id === id)?.name;
  return name ? <span className="mono">{name}</span> : <span className="mono dt-muted" title={id}>{id.slice(0, 8)}</span>;
}

export function SchemaOperationsActions() {
  const t = useT();
  const queryClient = useQueryClient();
  return <IconButton label={t("ops.refresh")} icon={RiRefreshLine} onClick={() => queryClient.invalidateQueries({ queryKey: ["schema-operations"] })} />;
}

/** The signed-in administrator's collection apply ledger, newest first; ?op= opens one. */
export function SchemaOperations() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useSearchState();
  const list = useInfiniteQuery(schemaOperationsQuery);
  const catalog = useQuery(collectionsQuery).data;
  const items = list.data?.pages.flatMap((page) => page.items) ?? [];
  const open = params.get("op");
  const close = () => update({ op: null });

  const table = (
    <>
      <div className="toolbar">
        {list.data && <span className="toolbar-count">{list.hasNextPage ? t("ops.logs.loadedMore", { count: items.length }) : t("ops.logs.count", { count: items.length })}</span>}
      </div>
      <div className="grid-scroll">
        {list.isPending ? (
          <Loader />
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={list.refetch} />
        ) : items.length === 0 ? (
          <EmptyState icon={RiHistoryLine}>{t("devtools.ops.empty")}</EmptyState>
        ) : (
          <>
            <table className="grid ops-grid" aria-label={t("nav.schemaOperations")}>
              <thead>
                <tr>
                  <th>{t("ops.logs.time")}</th>
                  <th>{t("devtools.ops.kind")}</th>
                  <th>{t("devtools.ops.target")}</th>
                  <th>{t("ops.logs.status")}</th>
                  <th className="num">{t("devtools.ops.version")}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr
                    key={item.operationId}
                    {...openRow(() => update({ op: item.operationId }), open === item.operationId)}
                    data-live={item.status === "running" || item.status === "unknown" ? "" : undefined}
                  >
                    <td className="mono ops-time">{time(item.createdAt)}</td>
                    <td>{t(`devtools.ops.kind.${item.operation}`)}</td>
                    <td><Target id={item.collectionId} catalog={catalog} /></td>
                    <td><StatusChip status={item.status} /></td>
                    <td className="num">{item.committedVersion === undefined ? item.baseVersion : `${item.baseVersion} → ${item.committedVersion}`}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            <LoadMore query={list} />
          </>
        )}
      </div>
    </>
  );

  return <SplitView list={table} paneLabel={t("devtools.ops.detail")} onClose={close} pane={open ? <OperationDetail key={open} id={open} catalog={catalog} onClose={close} /> : null} />;
}

function OperationDetail({ id, catalog, onClose }: { id: string; catalog: Collection[] | undefined; onClose: () => void }) {
  const t = useT();
  const time = useTimeFormat();
  const query = useQuery(schemaOperationQuery(id));
  const receipt: OperationReceipt | undefined = query.data;
  const at = (iso: string | undefined) => (iso ? <>{time(iso, true)} <span className="txt-hint mono">{iso}</span></> : null);
  const rows: [string, ReactNode][] = receipt
    ? [
        [t("devtools.ops.kind"), t(`devtools.ops.kind.${receipt.operation}`)],
        [t("devtools.ops.target"), <span className="mono">{catalog?.find((collection) => collection.id === receipt.collectionId)?.name ?? receipt.collectionId}</span>],
        [t("ops.logs.status"), <StatusChip status={receipt.status} />],
        [t("devtools.ops.failure"), receipt.failureCode ? <span className="dt-danger">{t(`devtools.ops.failure.${receipt.failureCode}`)} <span className="mono">({receipt.failureCode})</span></span> : null],
        [t("devtools.ops.baseVersion"), <span className="mono">{receipt.baseVersion}</span>],
        [t("devtools.ops.committedVersion"), receipt.committedVersion === undefined ? null : <span className="mono">{receipt.committedVersion}</span>],
        [t("devtools.ops.created"), at(receipt.createdAt)],
        [t("devtools.ops.updated"), at(receipt.updatedAt)],
        [t("devtools.ops.completed"), at(receipt.completedAt)],
      ]
    : [];
  return (
    <>
      <PaneHeader title={id} mono onClose={onClose} />
      <div className="pane-body">
        {query.isPending ? (
          <Loader />
        ) : query.isError ? (
          <ErrorState error={query.error} onRetry={query.refetch} />
        ) : (
          <>
            <Facts items={rows.map(([label, value]) => ({ label, value }))} />
            <h3 className="ops-subtitle">{t("ops.logs.raw")}</h3>
            <CodeBlock label={t("ops.logs.raw")} text={stringify(receipt, null, 2) ?? ""} />
          </>
        )}
      </div>
    </>
  );
}
