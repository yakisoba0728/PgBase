import { useEffect, useRef } from "react";
import { useT } from "../i18n";
import { CopyButton } from "./CopyButton";

// A long value is shown in full up to this many characters; copying always takes all of it.
const shownLimit = 100_000;

export interface Peek {
  label: string;
  text: string;
  /** The control that asked; the panel opens next to it and focus returns to it. */
  anchor: HTMLElement;
}

/** One shared panel showing the full text of a value a cell had to cut short (a grid renders a single instance). */
export function ValuePeek({ peek, onClose }: { peek: Peek | null; onClose: () => void }) {
  const t = useT();
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const element = panel.current;
    if (!element || !peek) return;
    const anchor = peek.anchor.getBoundingClientRect();
    const below = window.innerHeight - anchor.bottom > 240;
    element.style.top = below ? `${anchor.bottom + 4}px` : "";
    element.style.bottom = below ? "" : `${window.innerHeight - anchor.top + 4}px`;
    element.style.left = `${Math.max(8, Math.min(anchor.left, window.innerWidth - 488))}px`;
    if (!element.matches(":popover-open")) element.showPopover();
  }, [peek]);
  return (
    <div
      ref={panel}
      popover="auto"
      role="dialog"
      aria-label={peek?.label}
      className="popover peek"
      onToggle={(event) => {
        if (event.newState === "closed") {
          peek?.anchor.focus();
          onClose();
        }
      }}
      onClick={(event) => event.stopPropagation()}
    >
      {peek && (
        <>
          <div className="peek-head">
            <span className="peek-title">{peek.label}</span>
            <span className="txt-hint">{t("peek.length", { count: peek.text.length })}</span>
            <CopyButton label={t("peek.copy")} value={peek.text} />
          </div>
          <pre className="peek-body" tabIndex={0}>{peek.text.length > shownLimit ? `${peek.text.slice(0, shownLimit)}\n…` : peek.text}</pre>
        </>
      )}
    </div>
  );
}
