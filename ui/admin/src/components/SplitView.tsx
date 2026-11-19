import { useCallback, useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { RiCloseLine } from "@remixicon/react";
import { useSmallScreen } from "../app/shell";
import { useShortcut } from "../app/keys";
import { readStorage, writeStorage } from "../app/store";
import { useT } from "../i18n";
import { IconButton } from "./Button";
import { CopyButton } from "./CopyButton";

const storageKey = "pgbase.split";
// Kept in step with .split-pane's max-width (layout.css), which leaves the list at least minList.
const minPane = 380;
const minList = 320;
const focusable = "a[href], button, input, select, textarea, summary, [tabindex], [contenteditable='true']";

interface SplitViewProps {
  list: ReactNode;
  /** The detail pane; null closes it. */
  pane: ReactNode | null;
  paneLabel: string;
  /** Called on Escape while no dialog or menu is open. */
  onClose: () => void;
}

/** List on the left, a resizable detail pane on the right (full screen under 900px). */
export function SplitView({ list, pane, paneLabel, onClose }: SplitViewProps) {
  const root = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(() => Number(readStorage(storageKey)) || 520);
  const [dragging, setDragging] = useState(false);
  const open = pane !== null;
  const small = useSmallScreen();
  const panel = useRef<HTMLElement>(null);
  // The pane unmounts at once (its unmount drives the focus return above); a static, hidden copy plays the exit.
  const panelRef = useCallback((node: HTMLElement | null) => {
    panel.current = node;
    if (node) return () => leaveCopy(node, root.current);
  }, []);
  // At any width, closing the pane returns focus to where it was opened from, unless focus has moved on.
  useEffect(() => {
    if (!open) return;
    const previous = document.activeElement as HTMLElement | null;
    return () => {
      const lost = !document.activeElement || document.activeElement === document.body || !document.activeElement.isConnected;
      if (lost && previous?.isConnected) previous.focus();
    };
  }, [open]);
  useLayoutEffect(() => {
    const pane = panel.current;
    if (!open || !small || !pane) return;
    const previous = document.activeElement as HTMLElement | null;
    // Each sibling subtree of the pane and its ancestors is covered by the full-screen detail.
    const covered: Element[] = [];
    for (let child: Element = pane; child.parentElement && child !== document.body; child = child.parentElement) {
      for (const sibling of child.parentElement.children) {
        if (sibling === child || sibling.hasAttribute("inert")) continue;
        sibling.setAttribute("inert", "");
        covered.push(sibling);
      }
    }
    if (!pane.contains(document.activeElement)) (pane.querySelector<HTMLElement>("button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex='0']") ?? pane).focus();
    return () => {
      for (const sibling of covered) sibling.removeAttribute("inert");
      if (!pane.isConnected && previous?.isConnected) previous.focus();
    };
  }, [open, small]);

  useShortcut("Escape", onClose, { inFields: true, enabled: open && !small });

  const onPaneKey = (event: KeyboardEvent<HTMLElement>) => {
    if (!small || event.defaultPrevented || event.nativeEvent.isComposing || !(event.target instanceof HTMLElement)) return;
    // Native nested dialogs and popovers handle their own keyboard events.
    if (event.target.closest("dialog[open], [popover]")) return;
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key !== "Tab") return;
    const targets = [...event.currentTarget.querySelectorAll<HTMLElement>(focusable)].filter(
      (element) => element.tabIndex >= 0 && !element.matches(":disabled") && !element.closest("[inert]") && element.getClientRects().length,
    );
    const first = targets[0], last = targets.at(-1);
    if (!first || document.activeElement === event.currentTarget || (event.shiftKey ? document.activeElement === first : document.activeElement === last)) {
      event.preventDefault();
      (event.shiftKey ? last : first)?.focus();
    }
  };

  const clamp = (next: number) => {
    const total = root.current?.clientWidth ?? window.innerWidth;
    return Math.round(Math.max(minPane, Math.min(next, total - minList)));
  };
  const commit = (next: number) => {
    setWidth(next);
    writeStorage(storageKey, String(next));
  };

  const onPointerDown = (event: PointerEvent<HTMLDivElement>) => {
    event.currentTarget.setPointerCapture(event.pointerId);
    setDragging(true);
  };
  const onPointerMove = (event: PointerEvent<HTMLDivElement>) => {
    const right = root.current?.getBoundingClientRect().right;
    if (dragging && right !== undefined) setWidth(clamp(right - event.clientX));
  };
  const onPointerUp = () => {
    setDragging(false);
    commit(width);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowLeft" ? 24 : event.key === "ArrowRight" ? -24 : 0;
    if (!step) return;
    event.preventDefault();
    commit(clamp(width + step));
  };

  return (
    <div ref={root} className="split">
      <div className="split-list" inert={open && small}>{list}</div>
      {open && (
        <>
          <div
            className="split-divider"
            role="separator"
            aria-orientation="vertical"
            aria-label={paneLabel}
            aria-valuenow={width}
            tabIndex={0}
            data-dragging={dragging ? "" : undefined}
            onPointerDown={onPointerDown}
            onPointerMove={onPointerMove}
            onPointerUp={onPointerUp}
            onKeyDown={onKeyDown}
          />
          <section ref={panelRef} tabIndex={-1} className="split-pane" role={small ? "dialog" : undefined} aria-modal={small || undefined} aria-label={paneLabel} style={{ width }} onKeyDown={onPaneKey}>
            {pane}
          </section>
        </>
      )}
    </div>
  );
}

/**
 * Slides a closed pane out: an inert, aria-hidden copy (no ids or name, so queries never find two panes) takes the
 * pane's place and leaves. Only when the split view itself stays, i.e. the pane closed rather than the screen leaving.
 */
function leaveCopy(node: HTMLElement, split: HTMLElement | null) {
  const copy = node.cloneNode(true) as HTMLElement;
  const scrolled = [...node.querySelectorAll<HTMLElement>(".pane-body")].map((body) => body.scrollTop);
  queueMicrotask(() => {
    if (!split?.isConnected) return;
    copy.removeAttribute("role");
    copy.removeAttribute("aria-modal");
    copy.removeAttribute("aria-label");
    copy.removeAttribute("tabindex");
    for (const element of copy.querySelectorAll("[id]")) element.removeAttribute("id");
    copy.setAttribute("aria-hidden", "true");
    copy.inert = true;
    copy.classList.add("split-pane-leaving");
    split.append(copy);
    copy.querySelectorAll<HTMLElement>(".pane-body").forEach((body, index) => (body.scrollTop = scrolled[index] ?? 0));
    // A timer as well as animationend: a background tab never finishes the animation.
    const remove = () => copy.remove();
    copy.addEventListener("animationend", remove, { once: true });
    setTimeout(remove, 400);
  });
}

interface PaneHeaderProps {
  title: ReactNode;
  /** Ids and other machine values are set in monospace. */
  mono?: boolean;
  /** A value worth copying from the header (the record or user id). */
  copy?: { label: string; value: string };
  /** Extra header actions, placed before Close. */
  children?: ReactNode;
  /** Omitted for panes that are not dismissible. */
  onClose?: () => void;
}

/** The one detail-pane header: title, optional copy, actions, then Close (Esc does the same). */
export function PaneHeader({ title, mono, copy, children, onClose }: PaneHeaderProps) {
  const t = useT();
  return (
    <header className="pane-header">
      <h2 className={mono ? "pane-title mono" : "pane-title"}>{title}</h2>
      {copy && <CopyButton label={copy.label} value={copy.value} />}
      {children}
      {onClose && <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />}
    </header>
  );
}
