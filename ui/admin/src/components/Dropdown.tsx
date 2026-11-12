import { useId, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode, type ToggleEvent } from "react";
import { RiCheckLine, type RemixiconComponentType } from "@remixicon/react";

interface DropdownProps {
  label: string;
  icon: RemixiconComponentType;
  /** Shown as a muted heading inside the menu. */
  heading?: string;
  /** Visible text beside the icon; omitted, the button is icon-only. */
  text?: string;
  /** "up" opens above the button, left-aligned (for controls at the bottom of the screen). */
  placement?: "down" | "up";
  children: ReactNode;
}

/** A button that opens a menu in the top layer (Popover API: Escape and outside clicks close it). */
export function Dropdown({ label, icon: Icon, heading, text, placement = "down", children }: DropdownProps) {
  const id = useId();
  const button = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);

  const items = () => [...(menu.current?.querySelectorAll<HTMLElement>(".dropdown-item") ?? [])];

  // Positioned before it shows so it never flashes elsewhere.
  const onBeforeToggle = (event: ToggleEvent<HTMLDivElement>) => {
    const anchor = button.current?.getBoundingClientRect();
    if (event.newState !== "open" || !menu.current || !anchor) return;
    const style = menu.current.style;
    if (placement === "up") {
      style.bottom = `${window.innerHeight - anchor.top + 4}px`;
      style.left = `${Math.max(8, anchor.left)}px`;
    } else {
      style.top = `${anchor.bottom + 4}px`;
      style.right = `${Math.max(8, window.innerWidth - anchor.right)}px`;
    }
  };

  const onToggle = (event: ToggleEvent<HTMLDivElement>) => {
    setOpen(event.newState === "open");
    if (event.newState === "open") items()[0]?.focus();
  };

  const onKeyDown = (event: KeyboardEvent) => {
    const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
    if (!step) return;
    event.preventDefault();
    const list = items();
    const index = list.indexOf(document.activeElement as HTMLElement);
    list[(index + step + list.length) % list.length]?.focus();
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
    <button
      type="button"
      className="dropdown-item"
      role={checked === undefined ? "menuitem" : "menuitemradio"}
      aria-checked={checked}
      tabIndex={-1}
      onClick={onSelect}
    >
      {Icon && <Icon size={16} aria-hidden />}
      {children}
      {checked && <RiCheckLine className="check" size={16} aria-hidden />}
    </button>
  );
}
