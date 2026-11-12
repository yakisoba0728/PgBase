import { useEffect, useState, type RefObject } from "react";

/** The slice of fixed-height rows a scroll position shows, padded by `overscan` rows each side. */
export function visibleRange(scrollTop: number, viewport: number, rowHeight: number, count: number, overscan = 10): { start: number; end: number } {
  if (count === 0 || rowHeight <= 0) return { start: 0, end: 0 };
  const first = Math.floor(Math.max(0, scrollTop) / rowHeight);
  const last = Math.ceil((Math.max(0, scrollTop) + Math.max(viewport, rowHeight)) / rowHeight);
  return { start: Math.max(0, Math.min(first - overscan, count - 1)), end: Math.min(count, last + overscan) };
}

interface RowWindow {
  start: number;
  end: number;
  /** Space above and below the rendered rows, to keep the scrollbar honest. */
  before: number;
  after: number;
  /** True while the user is scrolling (for deferring per-row network work). */
  scrolling: boolean;
}

/** Windowing for a list of fixed-height rows inside `scroller`: only the visible slice is rendered. */
export function useVirtualRows(scroller: RefObject<HTMLElement | null>, count: number, rowHeight: number): RowWindow {
  const [view, setView] = useState({ top: 0, height: 800 });
  const [scrolling, setScrolling] = useState(false);

  useEffect(() => {
    const element = scroller.current;
    if (!element) return;
    let frame = 0;
    let idle = 0;
    const measure = () => {
      frame = 0;
      setView((previous) => (previous.top === element.scrollTop && previous.height === element.clientHeight ? previous : { top: element.scrollTop, height: element.clientHeight }));
    };
    const onScroll = () => {
      if (!frame) frame = requestAnimationFrame(measure);
      setScrolling(true);
      clearTimeout(idle);
      idle = window.setTimeout(() => setScrolling(false), 180);
    };
    measure();
    element.addEventListener("scroll", onScroll, { passive: true });
    const resize = new ResizeObserver(() => measure());
    resize.observe(element);
    return () => {
      element.removeEventListener("scroll", onScroll);
      resize.disconnect();
      cancelAnimationFrame(frame);
      clearTimeout(idle);
    };
  }, [scroller]);

  const { start, end } = visibleRange(view.top, view.height, rowHeight, count);
  return { start, end, before: start * rowHeight, after: Math.max(0, (count - end) * rowHeight), scrolling };
}
