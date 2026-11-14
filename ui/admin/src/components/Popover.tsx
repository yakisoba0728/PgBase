import { useId, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode, type ToggleEvent } from "react";
import { RiCheckLine, type RemixiconComponentType } from "@remixicon/react";

/**
 * Places a top-layer panel beside its anchor: below when there is room, otherwise above;
 * "end" aligns right edges (toolbar buttons), "start" aligns left edges (navigation, cells).
 */
export function placeNear(panel: HTMLElement, anchor: DOMRect, align: "start" | "end" = "end", width = 0) {
  const style = panel.style;
  const room = { below: window.innerHeight - anchor.bottom - 12, above: anchor.top - 12 };
  const below = room.below > 260 || room.below >= room.above;
  style.top = below ? `${anchor.bottom + 4}px` : "";
  style.bottom = below ? "" : `${window.innerHeight - anchor.top + 4}px`;
  // The open/close shift moves away from the anchor (overlays.css).
  panel.dataset.side = below ? "below" : "above";
  // Scales open from the corner nearest the anchor.
  style.transformOrigin = `${align === "end" ? "right" : "left"} ${below ? "top" : "bottom"}`;
  // Never past the viewport edge: the panel scrolls instead.
  style.maxHeight = `${Math.max(120, below ? room.below : room.above)}px`;
  style.right = align === "end" ? `${Math.max(8, window.innerWidth - anchor.right)}px` : "";
  style.left = align === "start" ? `${Math.max(8, Math.min(anchor.left, window.innerWidth - width - 8))}px` : "";
}

/** Shared by Popover and Dropdown: a button owning a popover="auto" panel (Escape and outside clicks close it). */
function useAnchored<T extends HTMLElement>(align: "start" | "end") {
  const id = useId();
  const button = useRef<HTMLButtonElement>(null);
  const panel = useRef<T>(null);
  const [open, setOpen] = useState(false);
  // Positioned before it shows so it never flashes elsewhere.
  const onBeforeToggle = (event: ToggleEvent<T>) => {
    const anchor = button.current?.getBoundingClientRect();
    if (event.newState === "open" && panel.current && anchor) placeNear(panel.current, anchor, align);
  };
  return { id, button, panel, open, setOpen, onBeforeToggle };
}

interface PopoverProps {
  label: string;
  icon: RemixiconComponentType;
  className?: string;
  buttonClassName?: string;
  /** Rendered only while open, so heavy content costs nothing when closed. */
  children: () => ReactNode;
}

/** An icon button with a non-modal panel (settings, help). */
export function Popover({ label, icon: Icon, className, buttonClassName = "icon-btn", children }: PopoverProps) {
  const { id, button, panel, open, setOpen, onBeforeToggle } = useAnchored<HTMLDivElement>("end");
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

interface DropdownProps {
  label: string;
  icon: RemixiconComponentType;
  /** Shown as a muted heading inside the menu. */
  heading?: string;
  /** Visible text beside the icon; omitted, the button is icon-only. */
  text?: string;
  /** "start" for controls in the navigation column. */
  align?: "start" | "end";
  children: ReactNode;
}

/** A button that opens a menu of actions or choices (arrow keys move, Enter picks). */
export function Dropdown({ label, icon: Icon, heading, text, align = "end", children }: DropdownProps) {
  const { id, button, panel: menu, open, setOpen, onBeforeToggle } = useAnchored<HTMLDivElement>(align);
  const items = () => [...(menu.current?.querySelectorAll<HTMLElement>(".dropdown-item") ?? [])];

  const onToggle = (event: ToggleEvent<HTMLDivElement>) => {
    setOpen(event.newState === "open");
    // Only when focus has not moved into the menu already (a fast keyboard choice must not be undone).
    if (event.newState === "open" && !menu.current?.contains(document.activeElement)) items()[0]?.focus();
  };

  const onKeyDown = (event: KeyboardEvent) => {
    const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
    if (!step) return;
    event.preventDefault();
    const list = items();
    const index = list.indexOf(document.activeElement as HTMLElement);
    list[(index + step + list.length) % list.length]?.focus();
  };

  // Arrow keys on the button open the menu and move into it at once, so a fast keyboard choice never waits for the toggle event.
  const onButtonKeyDown = (event: KeyboardEvent) => {
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const list = items();
    // Already open from a click: its first item counts as current even before focus has moved there.
    const open = menu.current?.matches(":popover-open") ?? false;
    if (!open) menu.current?.showPopover();
    list[event.key === "ArrowDown" ? (open ? 1 : 0) : list.length - 1]?.focus();
  };

  const onClick = (event: MouseEvent) => {
    if ((event.target as HTMLElement).closest(".dropdown-item")) {
      menu.current?.hidePopover();
      button.current?.focus();
    }
  };

  return (
    <>
      <button
        ref={button}
        type="button"
        className={text ? "dropdown-btn" : "icon-btn"}
        popoverTarget={id}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        title={text ? undefined : label}
        onKeyDown={onButtonKeyDown}
      >
        <Icon size={16} aria-hidden />
        {text && <span className="dropdown-btn-text">{text}</span>}
      </button>
      <div ref={menu} id={id} popover="auto" role="menu" aria-label={label} className="dropdown" onBeforeToggle={onBeforeToggle} onToggle={onToggle} onKeyDown={onKeyDown} onClick={onClick}>
        {heading && <div className="dropdown-label">{heading}</div>}
        {children}
      </div>
    </>
  );
}

interface DropdownItemProps {
  onSelect: () => void;
  icon?: RemixiconComponentType;
  /** Set for single-choice menus; renders a check on the chosen item. */
  checked?: boolean;
  children: ReactNode;
}

export function DropdownItem({ onSelect, icon: Icon, checked, children }: DropdownItemProps) {
  return (
    <button type="button" className="dropdown-item" role={checked === undefined ? "menuitem" : "menuitemradio"} aria-checked={checked} tabIndex={-1} onClick={onSelect}>
      {Icon && <Icon size={16} aria-hidden />}
      {children}
      {checked && <RiCheckLine className="check" size={16} aria-hidden />}
    </button>
  );
}
