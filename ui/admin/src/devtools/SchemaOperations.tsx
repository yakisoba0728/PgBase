import type { ReactNode } from "react";
import { windowOpen, windowShifted } from "../app/queries";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { Facts } from "../components/Facts";
import { RiHistoryLine } from "@remixicon/react";
import { stringify } from "lossless-json";
import type { Collection } from "../api/collections";
import type { OperationReceipt, OperationStatus } from "../api/schema";
import { Button, RefreshButton } from "../components/Button";
import { CodeBlock } from "../components/CodeBlock";
import { EmptyState, LoadMore, Notice, WindowNotice } from "../components/Feedback";
import { QueryView } from "../components/QueryView";
import { Row, Table, TableScroll } from "../components/Table";
import { SplitView, PaneHeader } from "../components/SplitView";
import { collectionsQuery } from "../collections/queries";
import { useT } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { schemaOperationQuery, schemaOperationsQuery } from "./queries";
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
  return <RefreshButton queryKey={["schema-operations"]} />;
}

/** The signed-in administrator's collection apply ledger, newest first; ?op= opens one. */
export function SchemaOperations() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useSearchState();
  const list = useInfiniteQuery(schemaOperationsQuery);
  const queryClient = useQueryClient();
  const shifted = windowShifted(list.data);
  const catalog = useQuery(collectionsQuery).data;
  const items = list.data?.pages.flatMap((page) => page.items) ?? [];
  const open = params.get("op");
  const close = () => update({ op: null });

  const table = (
    <>
      <div className="toolbar">
        {list.data && <span className="toolbar-count">{shifted ? t("ops.logs.window", { count: items.length }) : list.hasNextPage ? t("ops.logs.loadedMore", { count: items.length }) : t("ops.logs.count", { count: items.length })}</span>}
      </div>
      <TableScroll>
        <QueryView query={list} empty={<EmptyState icon={RiHistoryLine}>{t("devtools.ops.empty")}</EmptyState>} isEmpty={(data) => data.pages.every((page) => page.items.length === 0)}>
          {() => (
            <>
              {shifted && (
                <WindowNotice action={t("ops.logs.newest")} onReset={(button) => {
                  button.closest(".grid-scroll")?.scrollTo(0, 0);
                  void queryClient.resetQueries({ queryKey: schemaOperationsQuery.queryKey });
                }}>{t("ops.logs.shifted", { count: items.length })}</WindowNotice>
              )}
              <Table label={t("nav.schemaOperations")}>
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
                    <Row
                      key={item.operationId}
                      current={open === item.operationId}
                      onOpen={() => update({ op: item.operationId })}
                      data-live={item.status === "running" || item.status === "unknown" ? "" : undefined}
                    >
                      <td className="mono cell-time">{time(item.createdAt)}</td>
                      <td>{t(`devtools.ops.kind.${item.operation}`)}</td>
                      <td><Target id={item.collectionId} catalog={catalog} /></td>
                      <td><StatusChip status={item.status} /></td>
                      <td className="num">{item.committedVersion === undefined ? item.baseVersion : `${item.baseVersion} → ${item.committedVersion}`}</td>
                    </Row>
                  ))}
                </tbody>
              </Table>
              {/* Once the window is full each further page takes a click (see LogsView). */}
              <LoadMore query={list} auto={windowOpen(list.data)} />
            </>
          )}
        </QueryView>
      </TableScroll>
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
        [t("devtools.ops.failure"), receipt.failureCode ? <span className="txt-danger">{t(`devtools.ops.failure.${receipt.failureCode}`)} <span className="mono">({receipt.failureCode})</span></span> : null],
        [t("devtools.ops.baseVersion"), <span className="mono">{receipt.baseVersion}</span>],
        [t("devtools.ops.committedVersion"), receipt.committedVersion === undefined ? null : <span className="mono">{receipt.committedVersion}</span>],
        [t("devtools.ops.created"), at(receipt.createdAt)],
        [t("devtools.ops.updated"), at(receipt.updatedAt)],
        [t("devtools.ops.completed"), at(receipt.completedAt)],
      ]
    : [];
  return (
    <>
      <PaneHeader title={id} mono copy={{ label: t("common.copyId"), value: id }} onClose={onClose} />
      <div className="pane-body">
        <QueryView query={query} skeleton="form">
          {(data) => (
            <>
              <Facts items={rows.map(([label, value]) => ({ label, value }))} />
              <h3 className="subtitle">{t("ops.logs.raw")}</h3>
              <CodeBlock label={t("ops.logs.raw")} text={stringify(data, null, 2) ?? ""} />
            </>
          )}
        </QueryView>
      </div>
    </>
  );
}
