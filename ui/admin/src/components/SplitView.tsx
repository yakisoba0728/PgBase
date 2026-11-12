import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { readStorage, writeStorage } from "../app/store";

const storageKey = "pgbase.split";
const minPane = 340;
const minList = 280;

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

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented || overlayOpen()) return;
      onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

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
      <div className="split-list">{list}</div>
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
          <section className="split-pane" aria-label={paneLabel} style={{ width }}>
            {pane}
          </section>
        </>
      )}
    </div>
  );
}

function overlayOpen(): boolean {
  try {
    return document.querySelector("dialog[open], :popover-open") !== null;
  } catch {
    // Engines without :popover-open (jsdom) only have dialogs to check.
    return document.querySelector("dialog[open]") !== null;
  }
}
