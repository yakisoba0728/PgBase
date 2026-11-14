import { useEffect, useRef, useState, type ReactNode } from "react";
import { RiErrorWarningLine, RiRefreshLine, type RemixiconComponentType } from "@remixicon/react";
import { errorText } from "../app/queries";
import { useT } from "../i18n";
import { Button } from "./Button";

export type SkeletonVariant = "table" | "form" | "cards" | "editor" | "list";

/** Reserve the content's shape while its first response or screen code loads. */
export function Skeleton({ variant = "table" }: { variant?: SkeletonVariant }) {
  const t = useT();
  return (
    <div className={`skeleton skeleton-${variant}`} role="status" aria-busy="true">
      <span className="visually-hidden">{t("common.loading")}</span>
      <div className="skeleton-content" aria-hidden="true">
        {variant === "table" ? (
          <>
            <div className="skeleton-toolbar"><span /><span /></div>
            {Array.from({ length: 7 }, (_, row) => (
              <div key={row} className={`skeleton-row${row === 0 ? " skeleton-head" : ""}`}>
                {Array.from({ length: 5 }, (_, column) => <span key={column} />)}
              </div>
            ))}
          </>
        ) : variant === "form" ? (
          Array.from({ length: 4 }, (_, row) => <div key={row} className="skeleton-field"><span /><span /></div>)
        ) : variant === "cards" ? (
          Array.from({ length: 3 }, (_, row) => <div key={row} className="skeleton-card"><span /><span /></div>)
        ) : (
          Array.from({ length: variant === "editor" ? 9 : 5 }, (_, row) => <div key={row} className="skeleton-line"><span /></div>)
        )}
      </div>
    </div>
  );
}

export function EmptyState({ icon: Icon, children, action }: { icon?: RemixiconComponentType; children: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      {Icon && <Icon size={24} aria-hidden />}
      <p>{children}</p>
      {action}
    </div>
  );
}

/** A failed load, in words, with a retry (distinct from an empty result). */
export function ErrorState({ error, onRetry, busy, children }: { error?: unknown; onRetry: () => unknown; busy?: boolean; children?: ReactNode }) {
  const t = useT();
  return (
    <EmptyState icon={RiErrorWarningLine} action={<Button size="sm" variant="secondary" icon={RiRefreshLine} loading={busy} onClick={() => void onRetry()}>{t("common.retry")}</Button>}>
      {children ?? errorText(t, error)}
    </EmptyState>
  );
}

interface PagedQuery {
  hasNextPage: boolean;
  isFetchNextPageError: boolean;
  isFetchingNextPage: boolean;
  error: unknown;
  fetchNextPage: () => unknown;
}

/** The "Load more" footer of a cursor-paged list, with the failure and a retry in place; `auto` also loads as it scrolls into view. */
export function LoadMore({ query, auto, children }: { query: PagedQuery; auto?: boolean; children?: ReactNode }) {
  const t = useT();
  const [element, setElement] = useState<HTMLDivElement | null>(null);
  const latest = useRef(query.fetchNextPage);
  latest.current = query.fetchNextPage;
  const armed = Boolean(auto) && query.hasNextPage && !query.isFetchingNextPage && !query.isFetchNextPageError;
  useEffect(() => {
    // Without an observer (old engines) the button remains.
    if (!element || !armed || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => entries.some((entry) => entry.isIntersecting) && latest.current(), { rootMargin: "200px" });
    observer.observe(element);
    return () => observer.disconnect();
  }, [element, armed]);
  if (!query.hasNextPage && !query.isFetchNextPageError) return null;
  return (
    <div className="grid-foot" ref={setElement}>
      {children}
      {query.isFetchNextPageError && <span className="field-error" role="alert">{errorText(t, query.error)}</span>}
      <Button variant="secondary" size="sm" loading={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
        {query.isFetchNextPageError ? t("common.retry") : t("common.loadMore")}
      </Button>
    </div>
  );
}

interface NoticeProps {
  /** Warning when omitted. */
  tone?: "danger";
  title?: ReactNode;
  children?: ReactNode;
  actions?: ReactNode;
  /** "status" for progress that is not a problem. */
  role?: "alert" | "status";
  className?: string;
}

/** An inline message at the top of a pane or form: a problem, a risk or an undecided outcome. */
export function Notice({ tone, title, children, actions, role = "alert", className }: NoticeProps) {
  return (
    <div className={className ? `pane-notice ${className}` : "pane-notice"} data-tone={tone} role={role}>
      {title && <strong>{title}</strong>}
      {children && <p>{children}</p>}
      {actions && <div className="pane-notice-actions">{actions}</div>}
    </div>
  );
}
