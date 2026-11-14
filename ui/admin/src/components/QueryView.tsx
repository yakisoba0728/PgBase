import type { ReactNode } from "react";
import { useT } from "../i18n";
import { Button } from "./Button";
import { ErrorState, Notice, Skeleton, type SkeletonVariant } from "./Feedback";

/** The parts of a useQuery or useInfiniteQuery result QueryView reads. */
export interface ViewQuery<T> {
  data: T | undefined;
  isPending: boolean;
  isError: boolean;
  error: unknown;
  isFetching: boolean;
  refetch: () => unknown;
  /** Infinite queries: a failed next page is reported by LoadMore, not here. */
  isFetchNextPageError?: boolean;
}

interface QueryViewProps<T> {
  query: ViewQuery<T>;
  /** The shape reserved while the first response loads; "table" when omitted. */
  skeleton?: SkeletonVariant;
  /** Shown instead of children when `isEmpty(data)` holds. */
  empty?: ReactNode;
  /** When `empty` applies; an empty array when omitted. */
  isEmpty?: (data: T) => boolean;
  children: (data: T) => ReactNode;
}

/**
 * Two queries as one for QueryView: data is the pair once both have loaded (`null` counts as loaded), the error is
 * the one that left nothing to show (else any failed refresh), and retry refetches the failed ones.
 */
export function combineQueries<A, B>(a: ViewQuery<A>, b: ViewQuery<B>): ViewQuery<[A, B]> {
  const failed = [a, b].filter((query) => query.isError);
  return {
    data: a.data === undefined || b.data === undefined ? undefined : [a.data, b.data],
    isPending: a.isPending || b.isPending,
    isError: failed.length > 0,
    error: (failed.find((query) => query.data === undefined) ?? failed[0])?.error ?? null,
    isFetching: a.isFetching || b.isFetching,
    refetch: () => Promise.all(failed.map((query) => query.refetch())),
  };
}

const emptyArray =(data: unknown) => Array.isArray(data) && data.length === 0;

/**
 * The load lifecycle of one query: the skeleton until the first response, ErrorState when nothing has loaded,
 * then `empty` or `children(data)`. A failed refresh keeps the loaded data and says so above it, with a retry.
 *
 *   <QueryView query={jobs} empty={<EmptyState icon={RiTimerLine}>{t("ops.jobs.empty")}</EmptyState>} isEmpty={(data) => data.items.length === 0}>
 *     {(data) => <Table label={t("nav.jobs")}>…</Table>}
 *   </QueryView>
 */
export function QueryView<T>({ query, skeleton = "table", empty, isEmpty = emptyArray, children }: QueryViewProps<T>) {
  const t = useT();
  if (query.isPending) return <Skeleton variant={skeleton} />;
  // `data` may legitimately be null (a lookup that found nothing), so only undefined means "never loaded".
  if (query.data === undefined) return <ErrorState query={query} />;
  const data = query.data;
  const stale = query.isError && !query.isFetchNextPageError && (
    <Notice role="status" className="query-stale">
      {t("common.refreshFailed")}{" "}
      <Button size="sm" variant="transparent" loading={query.isFetching} onClick={() => void query.refetch()}>{t("common.retry")}</Button>
    </Notice>
  );
  return (
    <>
      {stale}
      {empty !== undefined && isEmpty(data) ? empty : children(data)}
    </>
  );
}
