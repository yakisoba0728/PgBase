import { useEffect, useRef } from "react";
import { useT } from "../i18n";
import { CopyButton } from "./CopyButton";
import { placeNear } from "./Popover";
import { tokenPx } from "./virtualRows";

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
    placeNear(element, peek.anchor.getBoundingClientRect(), "start", tokenPx("--peek-w", 540));
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
