import { useEffect } from "react";
import { useQueries, useQuery, useQueryClient, type UseQueryOptions } from "@tanstack/react-query";
import { ApiError } from "../api/http";
import { listSchemaOperations } from "../api/schema";
import { RunningOperations } from "../components/Nav";
import { toast } from "../components/Toast";
import type { ManagedJobList } from "../api/jobs";
import { importOperationQuery, schemaOperationQuery } from "../devtools/queries";
import { jobsQuery } from "../ops/queries";
import { useT, type MessageKey } from "../i18n";
import { backupJobQuery, migrationQuery, migrationRunning } from "../storage/queries";
import { findOperation, isWatched, operations, operationState, pollWhileRunning, trackOperation, untrackOperation, useOperations, type OperationKind, type TrackedOperation } from "./operations";

const isNotFound = (error: unknown) => error instanceof ApiError && error.status === 404;

function queryFor(operation: TrackedOperation): UseQueryOptions<unknown> {
  switch (operation.kind) {
    case "backup":
    case "restore":
      return backupJobQuery(operation.id) as UseQueryOptions<unknown>;
    case "migration":
      return migrationQuery(operation.id) as UseQueryOptions<unknown>;
    case "import":
      return { ...importOperationQuery(operation.id), refetchInterval: pollWhileRunning } as UseQueryOptions<unknown>;
    case "job":
      return { ...jobsQuery, select: (list: ManagedJobList) => list.items.find((job) => job.id === operation.id) ?? null, refetchInterval: 3000 } as UseQueryOptions<unknown>;
    case "schema":
      // The page polls "unknown" too; from the navigation it only waits for a resend.
      return { ...schemaOperationQuery(operation.id), refetchInterval: pollWhileRunning } as UseQueryOptions<unknown>;
  }
}

const hrefs: Record<OperationKind, (operation: TrackedOperation) => string> = {
  backup: (operation) => `/backups?job=${encodeURIComponent(operation.id)}`,
  restore: (operation) => `/backups?job=${encodeURIComponent(operation.id)}`,
  migration: (operation) => `/storage/migration?migration=${encodeURIComponent(operation.id)}`,
  import: () => "/schema-transfer/import",
  schema: (operation) => `/schema-operations?op=${encodeURIComponent(operation.id)}`,
  job: (operation) => `/jobs?job=${encodeURIComponent(operation.id)}`,
};

// Discovery runs once per tab session (a reload re-attaches from storage); the latest-migration
// endpoint answers 404 when no migration ever ran, which would otherwise log on every load.
function firstInSession(): boolean {
  try {
    if (sessionStorage.getItem("pgbase.operations.discovered")) return false;
    sessionStorage.setItem("pgbase.operations.discovered", "1");
  } catch {
    // Without session storage discovery runs on every load.
  }
  return true;
}
const discover = firstInSession();

/** What the server may have started without this browser: the latest file migration and own running schema applies. */
function useDiscovery() {
  const migration = useQuery({ ...migrationQuery(null), refetchInterval: false, staleTime: Infinity, enabled: discover });
  const schema = useQuery({ queryKey: ["schema-operations", "discovery"], queryFn: ({ signal }) => listSchemaOperations({ limit: 20, signal }), staleTime: Infinity, retry: false, enabled: discover });
  useEffect(() => {
    const latest = migration.data;
    if (latest && migrationRunning(latest.status) && !findOperation("migration")) trackOperation({ kind: "migration", id: latest.id });
  }, [migration.data]);
  useEffect(() => {
    for (const item of schema.data?.items ?? []) {
      if (item.status === "running" && !operations().some((known) => known.id === item.operationId)) trackOperation({ kind: "schema", id: item.operationId });
    }
  }, [schema.data]);
}

/** The navigation's running-operations list: polls each tracked operation and reports its outcome once. */
export function RunningFeed() {
  const t = useT();
  const queryClient = useQueryClient();
  const list = useOperations();
  useDiscovery();
  // A screen settling its own operation polls it already; the feed waits until it lets go.
  const results = useQueries({ queries: list.map((operation) => ({ ...queryFor(operation), enabled: !isWatched(operation.id) })) });
  const states = list.map((operation, index) => operationState(operation, results[index]?.data as Parameters<typeof operationState>[1], isNotFound(results[index]?.error)));
  const signature = states.map((entry, index) => `${list[index].id}:${entry.state}`).join(",");

  useEffect(() => {
    list.forEach((operation, index) => {
      const { state } = states[index];
      if (state === "running" || state === "attention" || isWatched(operation.id)) return;
      if (!untrackOperation(operation.id) || state === "gone") return;
      const kind = operation.kind;
      const name = (kind === "schema" || kind === "job") && operation.subject ? ` · ${operation.subject}` : "";
      if (state === "done") toast(t(`ops.running.done.${kind}` as MessageKey) + name);
      else toast(t(`ops.running.failed.${kind}` as MessageKey) + name, "danger");
      // A restore swaps the whole project: every cached screen is stale.
      if (kind === "restore" && state === "done") void queryClient.invalidateQueries();
      else if (kind === "backup" || kind === "restore") void queryClient.invalidateQueries({ queryKey: ["backups"] });
      else if (kind === "migration") void queryClient.invalidateQueries({ queryKey: ["storage"] });
      else if (kind === "job") void queryClient.invalidateQueries({ queryKey: jobsQuery.queryKey });
      else {
        void queryClient.invalidateQueries({ queryKey: ["collections"] });
        void queryClient.invalidateQueries({ queryKey: ["schema-operations"] });
      }
    });
  }, [signature]); // the list and states are read fresh from this render

  const items = list.map((operation, index) => {
    const { state, progress } = states[index];
    return {
      id: operation.id,
      href: hrefs[operation.kind](operation),
      label: t(`ops.running.${operation.kind}` as MessageKey) + ((operation.kind === "schema" || operation.kind === "job") && operation.subject ? ` · ${operation.subject}` : ""),
      progress: state === "attention" ? t("ops.running.attention") : progress,
      attention: state === "attention",
    };
  });
  return <RunningOperations label={t("nav.running")} items={items} />;
}
