import { useId, useRef, useState, type ReactNode, type ToggleEvent } from "react";
import type { RemixiconComponentType } from "@remixicon/react";

interface PopoverProps {
  label: string;
  icon: RemixiconComponentType;
  className?: string;
  buttonClassName?: string;
  /** Rendered only while open, so heavy content costs nothing when closed. */
  children: () => ReactNode;
}

/** An icon button with a non-modal panel in the top layer (Popover API: Escape and outside clicks close it). */
export function Popover({ label, icon: Icon, className, buttonClassName = "icon-btn", children }: PopoverProps) {
  const id = useId();
  const button = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);

  // Right-aligned under the button, or above it when there is no room below.
  const onBeforeToggle = (event: ToggleEvent<HTMLDivElement>) => {
    const anchor = button.current?.getBoundingClientRect();
    if (event.newState !== "open" || !panel.current || !anchor) return;
    const style = panel.current.style;
    const below = window.innerHeight - anchor.bottom > 260 || anchor.top < window.innerHeight / 2;
    style.top = below ? `${anchor.bottom + 4}px` : "";
    style.bottom = below ? "" : `${window.innerHeight - anchor.top + 4}px`;
    style.right = `${Math.max(8, window.innerWidth - anchor.right)}px`;
  };

  return (
    <>
      <button ref={button} type="button" className={buttonClassName} popoverTarget={id} aria-expanded={open} aria-label={label} title={label} onClick={(event) => event.stopPropagation()}>
        <Icon size={16} aria-hidden />
      </button>
      <div
        ref={panel}
        id={id}
        popover="auto"
        role="dialog"
        aria-label={label}
        className={className ? `popover ${className}` : "popover"}
        onBeforeToggle={onBeforeToggle}
        onToggle={(event) => setOpen(event.newState === "open")}
        onClick={(event) => event.stopPropagation()}
      >
        {open && children()}
      </div>
    </>
  );
}
