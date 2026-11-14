import { useEffect, useRef, useState, type ComponentProps, type KeyboardEvent, type MouseEvent } from "react";

/**
 * The list tables. A screen writes its own <thead>, <tbody> and plain <td> cells inside these:
 *
 *   <TableScroll>                          // the scrolling list body: skeletons, notices and LoadMore go in it too
 *     <Table label={t("nav.jobs")}>
 *       <thead><tr><th>…</th></tr></thead>
 *       <tbody>
 *         {items.map((job) => <Row key={job.id} current={job.id === openId} onOpen={() => update({ job: job.id })}><td>…</td></Row>)}
 *       </tbody>
 *     </Table>
 *   </TableScroll>
 */

const join = (base: string, extra?: string) => (extra ? `${base} ${extra}` : base);

/** The scroll area of a list screen (`.grid-scroll`); the sticky header casts its shadow once rows pass under it. */
export function TableScroll({ className, ...rest }: ComponentProps<"div">) {
  return <div {...rest} className={join("grid-scroll", className)} />;
}

/** A `.grid` table; `label` names it for assistive technology. */
export function Table({ label, className, ...rest }: Omit<ComponentProps<"table">, "aria-label"> & { label?: string }) {
  return <table {...rest} className={join("grid", className)} aria-label={label} />;
}

// Clicks on these inside a row do their own thing (a link opens, a checkbox toggles) and never open the row.
const inner = "a, button, input, select, textarea, label, summary, [role='button'], [contenteditable='true']";

/** Whether a click on a row should open it: a plain primary click on the row itself, not on a control or link in it. */
export function opensRow(event: MouseEvent<HTMLElement>): boolean {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return false;
  // The end of a drag that selected text in a cell, not a click.
  const selection = window.getSelection();
  if (selection && !selection.isCollapsed && event.currentTarget.contains(selection.focusNode)) return false;
  const control = (event.target as Element).closest(inner);
  return control === null || !event.currentTarget.contains(control);
}

interface RowProps extends Omit<ComponentProps<"tr">, "onClick"> {
  /** Opens the row's item (usually by setting a search param). */
  onOpen: () => void;
  /** The row whose item is open: aria-current and the accent background. */
  current?: boolean;
}

/**
 * A table row that opens its item: a click on the row, or Enter/Space while the row itself has focus. Modifier and
 * middle clicks, clicks on links, buttons and inputs inside it, and keys pressed in those controls are left alone,
 * so a cell can hold a real link (Cmd/Ctrl+click opens a tab) or a button without stopping propagation.
 * `onKeyDown` runs first and can preventDefault to keep a key; `tabIndex` defaults to 0.
 */
export function Row({ onOpen, current, onKeyDown, ...rest }: RowProps) {
  return (
    <tr
      ref={arrive}
      tabIndex={0}
      {...rest}
      aria-current={current ? "true" : undefined}
      onClick={(event) => opensRow(event) && onOpen()}
      onKeyDown={(event: KeyboardEvent<HTMLTableRowElement>) => {
        onKeyDown?.(event);
        if (event.defaultPrevented || event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
        event.preventDefault();
        onOpen();
      }}
    />
  );
}

/** At most this many rows arriving together are an item created or restored; more is a page, filter or reload. */
const fewArrivals = 2;
const bodies = new WeakMap<Element, { since: number; batch: HTMLElement[] }>();

/** Marks the row for the CSS wash (`data-arrived`) and clears it once played (a timer too, for hidden tabs). */
function mark(row: HTMLElement) {
  row.setAttribute("data-arrived", "");
  setTimeout(() => row.removeAttribute("data-arrived"), 1200);
}

/**
 * Staggered entrance (base.css): each row fades in one step after the one above it, capped at 8 steps. Only rows
 * that just mounted are marked, so rows already on screen never replay it; cleared once played (a timer, for hidden tabs).
 */
export function enterRows(rows: HTMLElement[]) {
  rows.forEach((row, index) => {
    row.style.setProperty("--enter-i", String(Math.min(index, 8)));
    row.setAttribute("data-enter", "");
    setTimeout(() => row.removeAttribute("data-enter"), 1000);
  });
}

/**
 * Row's arrival: a row that mounts into a table body already on screen, alone or with one other (a created item, a
 * retried event), gets the wash; the body's first rows, and a page or filter result arriving together, enter staggered.
 */
function arrive(row: HTMLTableRowElement | null) {
  const body = row?.parentElement;
  if (!row || !body) return;
  let seen = bodies.get(body);
  if (!seen) bodies.set(body, (seen = { since: performance.now(), batch: [] }));
  // StrictMode attaches each ref twice; a row counts once.
  if (seen.batch.includes(row)) return;
  seen.batch.push(row);
  if (seen.batch.length > 1) return;
  const batch = seen;
  queueMicrotask(() => {
    // Rows of the body's own first render (and its StrictMode replay) mount within the same moment.
    const initial = performance.now() - batch.since < 300;
    if (!initial && batch.batch.length <= fewArrivals) batch.batch.forEach(mark);
    else enterRows(batch.batch);
    batch.batch = [];
  });
}

/**
 * The same wash for a windowed table (records), whose rows also mount while scrolling: decided by id, not by mount.
 * Returns the ids that newly appeared in `ids` (a few at most), for about a second.
 */
export function useArrivals(ids: string[]): Set<string> {
  const known = useRef<Set<string> | null>(null);
  const [arrived, setArrived] = useState(noArrivals);
  const key = ids.join("\n");
  useEffect(() => {
    const previous = known.current;
    known.current = new Set(ids);
    const fresh = previous ? ids.filter((id) => !previous.has(id)) : [];
    if (fresh.length === 0 || fresh.length > fewArrivals) return;
    setArrived(new Set(fresh));
    const timer = setTimeout(() => setArrived(noArrivals), 1200);
    return () => {
      clearTimeout(timer);
      setArrived(noArrivals);
    };
    // `key` stands for `ids`: a refetch with the same ids is no change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  return arrived;
}

const noArrivals = new Set<string>();
