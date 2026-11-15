import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { RiAlertLine, RiErrorWarningLine, RiInformationLine, RiRefreshLine, type RemixiconComponentType } from "@remixicon/react";
import { errorText } from "../app/queries";
import { useT } from "../i18n";
import { Button } from "./Button";
import { CopyButton } from "./CopyButton";

/**
 * The shape a skeleton reserves: "table" a list (its toolbar too, outside a TableScroll), "form" stacked fields,
 * "sections" FormSections (title column and fields), "facts" Facts rows, "pane" a detail pane (header and fields),
 * "cards" stat cards, "editor" a code or text area, "list" short lines (a nav or picker list).
 */
export type SkeletonVariant = "table" | "form" | "sections" | "facts" | "pane" | "cards" | "editor" | "list";

const repeat = (count: number, make: (index: number) => ReactNode) => Array.from({ length: count }, (_, index) => make(index));
// Label and control, in the field's own grid gap.
const fields = (count: number) => <div className="form">{repeat(count, (row) => <div key={row} className="skeleton-field"><span /><span /></div>)}</div>;

// When the skeleton now on screen first showed (index.html's boot skeleton counts from page start).
let shownSince = 0;

/** Reserve the content's shape while its first response or screen code loads; `className` places it (the boot skeleton). */
export function Skeleton({ variant = "table", className }: { variant?: SkeletonVariant; className?: string }) {
  const t = useT();
  // Read before this one commits: a skeleton still on screen is being replaced, so this one carries on its reveal.
  const [since] = useState(() => {
    const now = performance.now();
    if (!document.querySelector(".skeleton")) shownSince = now;
    return shownSince - now;
  });
  return (
    <div className={`skeleton skeleton-${variant}${className ? ` ${className}` : ""}`} role="status" aria-busy="true" style={{ "--skeleton-since": `${since}ms` } as CSSProperties}>
      <span className="visually-hidden">{t("common.loading")}</span>
      <div className="skeleton-content" aria-hidden="true">
        {variant === "table" ? (
          <>
            <div className="skeleton-toolbar"><span /><span /></div>
            {repeat(7, (row) => <div key={row} className={`skeleton-row${row === 0 ? " skeleton-head" : ""}`}>{repeat(5, (column) => <span key={column} />)}</div>)}
          </>
        ) : variant === "form" ? (
          fields(4)
        ) : variant === "sections" ? (
          // The real FormSection grid (and its narrow-screen stacking), so titles and fields land where they will be.
          repeat(2, (row) => <div key={row} className="form-section"><header className="skeleton-heading"><span /><span /></header>{fields(3)}</div>)
        ) : variant === "facts" ? (
          <div className="facts">{repeat(7, (row) => <div key={row}><span /><span /></div>)}</div>
        ) : variant === "pane" ? (
          <>
            <div className="pane-header"><span /></div>
            <div className="pane-body">{fields(4)}</div>
          </>
        ) : variant === "cards" ? (
          repeat(5, (row) => <div key={row} className="skeleton-card"><span /><span /></div>)
        ) : (
          repeat(variant === "editor" ? 9 : 5, (row) => <div key={row} className="skeleton-line"><span /></div>)
        )}
      </div>
    </div>
  );
}

/** Nothing to show: a muted icon, one line, and the screen's own primary action when it has one. */
export function EmptyState({ icon: Icon, children, action, tone }: { icon?: RemixiconComponentType; children: ReactNode; action?: ReactNode; tone?: "danger" }) {
  return (
    <div className="empty" data-tone={tone}>
      {Icon && <Icon className="empty-icon" size={20} aria-hidden />}
      <p>{children}</p>
      {action && <div className="empty-action">{action}</div>}
    </div>
  );
}

interface UnavailableProps {
  icon: RemixiconComponentType;
  title: ReactNode;
  /** What the feature does and why it is off (the paragraph `textId` names). */
  children: ReactNode;
  /** The configuration it needs (an environment variable or option name), shown as code with a copy button. */
  setting?: string;
  /** How to turn it on; it refers to `setting` rather than spelling it out. */
  how: ReactNode;
  /** Lets disabled actions elsewhere cite the reason. */
  textId?: string;
  action?: ReactNode;
}

/**
 * An opt-in feature this server runs without (its routes answer 404): what it is and how to turn it on.
 * No retry, since asking again cannot change it.
 */
export function Unavailable({ icon: Icon, title, children, setting, how, textId, action }: UnavailableProps) {
  const t = useT();
  return (
    <div className="unavailable">
      <span className="unavailable-icon" aria-hidden><Icon size={18} /></span>
      <div className="unavailable-body">
        <h2>{title}</h2>
        <p id={textId}>{children}</p>
        {setting && (
          <div className="unavailable-setting">
            <code>{setting}</code>
            <CopyButton size="xs" label={t("common.copySetting")} value={setting} />
          </div>
        )}
        <p className="unavailable-how">{how}</p>
        {action && <div className="unavailable-action">{action}</div>}
      </div>
    </div>
  );
}

interface FailedQuery {
  error: unknown;
  isFetching: boolean;
  refetch: () => unknown;
}

/** A failed load, in words, with a retry (distinct from an empty result); `query` shows that query's error and retries it. */
export function ErrorState(props: ({ query: FailedQuery } | { error?: unknown; onRetry: () => unknown; busy?: boolean }) & { children?: ReactNode }) {
  const t = useT();
  const { error, onRetry, busy } = "query" in props ? { error: props.query.error, onRetry: props.query.refetch, busy: props.query.isFetching } : props;
  const { children } = props;
  return (
    <EmptyState icon={RiErrorWarningLine} tone="danger" action={<Button size="sm" variant="secondary" icon={RiRefreshLine} loading={busy} onClick={() => void onRetry()}>{t("common.retry")}</Button>}>
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

/** Shown once a scrolled list's window has moved past its first page; the button goes back to the start. */
export function WindowNotice({ children, action, onReset }: { children: ReactNode; action: string; onReset: (button: HTMLButtonElement) => void }) {
  return (
    <div className="list-window" role="status">
      <span>{children}</span>
      <Button size="sm" variant="secondary" onClick={(event) => onReset(event.currentTarget)}>{action}</Button>
    </div>
  );
}

interface NoticeProps {
  /** Warning when omitted; "info" for neutral news (a request sent, a restore finished). */
  tone?: "danger" | "info";
  title?: ReactNode;
  /** Text or block content (paragraphs, lists, code). */
  children?: ReactNode;
  actions?: ReactNode;
  /** "status" for progress that is not a problem, "note" for a standing caveat. */
  role?: "alert" | "status" | "note";
  className?: string;
}

const noticeIcons = { warning: RiAlertLine, danger: RiErrorWarningLine, info: RiInformationLine };

/** The one inline message box: a problem, a risk, a caveat or an undecided outcome, at the top of what it concerns. */
export function Notice({ tone, title, children, actions, role = "alert", className }: NoticeProps) {
  const Icon = noticeIcons[tone ?? "warning"];
  return (
    <div className={className ? `notice ${className}` : "notice"} data-tone={tone ?? "warning"} role={role}>
      <Icon className="notice-icon" size={16} aria-hidden />
      <div className="notice-text">
        {title && <strong>{title}</strong>}
        {children && <div className="notice-body">{children}</div>}
        {actions && <div className="notice-actions">{actions}</div>}
      </div>
    </div>
  );
}
