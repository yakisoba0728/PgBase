import { useLayoutEffect, useRef, type RefObject } from "react";
import { reducedMotion } from "../app/transitions";

/**
 * FLIP for a reorderable list: children of `list` carrying `data-flip` (a stable key) that moved since the last
 * `order` glide from their old place to the new one on the spring (tokens.css). Offsets are measured within the list,
 * so scrolling between renders is never mistaken for a move; reduced motion skips it.
 */
export function useFlip(list: RefObject<HTMLElement | null>, order: string) {
  const last = useRef(new Map<string, number>());
  useLayoutEffect(() => {
    const items = [...(list.current?.querySelectorAll<HTMLElement>("[data-flip]") ?? [])];
    const next = new Map(items.map((item) => [item.dataset.flip ?? "", item.offsetTop]));
    const previous = last.current;
    last.current = next;
    if (!previous.size || reducedMotion() || typeof Element.prototype.animate !== "function") return;
    const tokens = getComputedStyle(document.documentElement);
    const duration = parseFloat(tokens.getPropertyValue("--dur-slow")) || 320;
    const easing = tokens.getPropertyValue("--ease-spring").trim() || "ease-out";
    for (const item of items) {
      const from = previous.get(item.dataset.flip ?? "");
      const shift = from === undefined ? 0 : from - item.offsetTop;
      if (shift) item.animate([{ translate: `0 ${shift}px` }, { translate: "0 0" }], { duration, easing });
    }
  }, [list, order]);
}
