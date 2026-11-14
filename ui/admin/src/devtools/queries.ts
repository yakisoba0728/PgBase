import { infiniteQueryOptions, queryOptions, type InfiniteData, type QueryClient } from "@tanstack/react-query";
import { pageWindow } from "../app/queries";
import { applySchemaImport, exportSchema, getSchemaImportOperation, getSchemaOperation, listSchemaOperations, type SchemaImportResult } from "../api/schema";
import { getSqlExecution, listSqlExecutions } from "../api/sql";
import { ApiError } from "../api/http";
import { Undecided } from "../schema/apply";
import { sqlActive, type PendingImport } from "./model";

const undecided = (status: string) => status === "running" || status === "unknown";

export const exportQuery = queryOptions({
  queryKey: ["schema-export"],
  queryFn: ({ signal }) => exportSchema(signal),
  gcTime: 0,
});

export const schemaOperationsQuery = infiniteQueryOptions({
  queryKey: ["schema-operations", "list"],
  queryFn: ({ pageParam, signal }) => listSchemaOperations({ limit: 50, cursor: pageParam, signal }),
  ...pageWindow,
  getNextPageParam: (page) => page.nextCursor,
  // Undecided operations settle on their own; keep the list current until they do.
  refetchInterval: (query) => (query.state.data?.pages.some((page) => page.items.some((item) => undecided(item.status))) ? 2000 : false),
});

export const schemaOperationQuery = (id: string) =>
  queryOptions({
    queryKey: ["schema-operations", "one", id],
    queryFn: ({ signal }) => getSchemaOperation(id, signal),
    refetchInterval: (query) => (query.state.data && undecided(query.state.data.status) ? 2000 : false),
  });

/** An import apply by operation id; "unknown" is not polled, since only a resend moves it. */
export const importOperationQuery = (id: string) =>
  queryOptions({
    queryKey: ["schema-import", id],
    queryFn: ({ signal }) => getSchemaImportOperation(id, signal),
    refetchInterval: (query) => (query.state.data?.status === "running" ? 2000 : false),
  });

export const sqlHistoryKey = ["sql", "history"] as const;

export const sqlHistoryQuery = infiniteQueryOptions({
  queryKey: sqlHistoryKey,
  queryFn: ({ pageParam, signal }) => listSqlExecutions(pageParam, signal),
  ...pageWindow,
  getNextPageParam: (page) => page.nextCursor,
});

/**
 * Brings the SQL history back to its newest page. Its window may have moved past it (a plain refetch starts at the
 * oldest page kept), and a reset would unmount the workbench with the editor, so the shown data stays until the refetch lands.
 */
export async function refreshSqlHistory(queryClient: QueryClient, toNewest: boolean) {
  type History = InfiniteData<unknown, string | undefined>;
  const original = toNewest ? queryClient.getQueryData<History>(sqlHistoryKey) : undefined;
  // The kept page is a middle one, not the newest: it may only stand in while the newest is fetched.
  const trimmed = original && queryClient.setQueryData<History>(sqlHistoryKey, { pages: original.pages.slice(0, 1), pageParams: [undefined] });
  await queryClient.invalidateQueries({ queryKey: sqlHistoryKey });
  // A failed refetch leaves the stand-in: put the real window (and its notice) back; the error stays on the query.
  const query = queryClient.getQueryCache().find<History>({ queryKey: sqlHistoryKey, exact: true });
  if (trimmed && query?.state.status === "error" && query.state.data === trimmed) query.setState({ data: original });
}

export const sqlExecutionQuery = (id: string) =>
  queryOptions({
    queryKey: ["sql", "execution", id],
    queryFn: ({ signal }) => getSqlExecution(id, signal),
    refetchInterval: (query) => (query.state.data && sqlActive(query.state.data.status) ? 400 : false),
  });

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** Applies (or, with the same identity, resumes) an import and waits briefly for its outcome. */
export async function runImport(pending: PendingImport): Promise<SchemaImportResult> {
  const { document, baseVersion, checksum, identity } = pending;
  const apply = () => applySchemaImport({ document, baseVersion, checksum }, identity);
  let result = await apply();
  for (let attempt = 0; undecided(result.status); attempt++) {
    if (attempt >= 15) throw new Undecided(identity.operationId);
    await sleep(1000);
    // "unknown" is a claim left before execution: only resending the same request resumes it.
    if (result.status === "unknown") result = await apply();
    else {
      try {
        result = await getSchemaImportOperation(identity.operationId);
      } catch {
        // A failed status read says nothing about the accepted apply.
        throw new Undecided(identity.operationId);
      }
    }
  }
  if (result.status === "failed") throw new ApiError(409, "schema_operation_failed", result.failureCode ?? "failed");
  return result;
}
