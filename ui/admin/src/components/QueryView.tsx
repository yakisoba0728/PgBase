import { useState, type ReactNode } from "react";
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
  /** From useHeld: `data` is the previous answer, kept on screen while this one loads. */
  isHeld?: boolean;
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
    isHeld: Boolean(a.isHeld || b.isHeld),
    refetch: () => Promise.all(failed.map((query) => query.refetch())),
  };
}

/**
 * Keeps a query's last answer on screen while it loads again for the same `entity`: new parameters (filter, sort,
 * range) or a reset after a change. The result then reads as loaded with `isHeld` set, and QueryView dims it under a
 * progress line instead of showing the skeleton. Another entity (collection, user) starts blank, as does a failure.
 *
 *   const records = useHeld(useInfiniteQuery(recordsListQuery(collection, url.filter, url.sort, columns.visible)), collection.name);
 */
export function useHeld<R extends { data: unknown; isPending: boolean; isFetching: boolean }>(result: R, entity: unknown = null): R & { isHeld?: boolean } {
  const [held, setHeld] = useState<{ data: R["data"]; entity: unknown }>();
  if (result.data !== undefined && (held?.data !== result.data || !Object.is(held.entity, entity))) setHeld({ data: result.data, entity });
  // Pending without fetching is a disabled query; a failure is pending no more.
  if (!result.isPending || !result.isFetching || !held || !Object.is(held.entity, entity)) return result;
  return { ...result, data: held.data, isPending: false, isHeld: true };
}

// Once per hold (a stable ref): a list loading anew starts at its top, as it did when the skeleton replaced it.
const toTop = (element: HTMLElement | null) => element?.closest(".grid-scroll")?.scrollTo({ top: 0 });

const emptyArray = (data: unknown) => Array.isArray(data) && data.length === 0;

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
  // Out of flow and before the content, so the content keeps its place in the parent and CSS dims it as a sibling.
  const busy = query.isHeld && <div className="query-busy" role="progressbar" aria-busy="true" aria-label={t("common.loading")} ref={toTop} />;
  return (
    <>
      {stale}
      {busy}
      {empty !== undefined && isEmpty(data) ? empty : children(data)}
    </>
  );
}
