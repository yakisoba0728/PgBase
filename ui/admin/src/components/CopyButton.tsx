import { useEffect, useState, type MouseEvent } from "react";
import { RiCheckLine, RiFileCopyLine } from "@remixicon/react";
import { tNow, useT } from "../i18n";
import { Button, IconButton } from "./Button";
import { toast } from "./Toast";

/** Copies text to the clipboard with the one shared "Copied." / "Could not copy." feedback. */
export function copyText(text: string): Promise<boolean> {
  return Promise.resolve().then(() => navigator.clipboard.writeText(text)).then(
    () => {
      toast(tNow("copy.done"));
      return true;
    },
    () => {
      toast(tNow("copy.failed"), "danger");
      return false;
    },
  );
}

interface CopyButtonProps {
  /** The value, or a function so large values are only built on click. */
  value: string | (() => string);
  /** Accessible name; defaults to "Copy". */
  label?: string;
  /** Shows the label beside the icon (a secondary button) instead of an icon-only button. */
  text?: boolean;
  /** Icon-only size, as for IconButton. */
  size?: "xs";
  className?: string;
}

/** The single copy affordance: the icon turns into a check for a moment after a successful copy. */
export function CopyButton({ value, label, text, size, className }: CopyButtonProps) {
  const t = useT();
  const [done, setDone] = useState(false);
  useEffect(() => {
    if (!done) return;
    const timer = window.setTimeout(() => setDone(false), 1500);
    return () => window.clearTimeout(timer);
  }, [done]);
  const name = label ?? t("common.copy");
  const icon = done ? RiCheckLine : RiFileCopyLine;
  const onClick = (event: MouseEvent) => {
    event.stopPropagation();
    void copyText(typeof value === "function" ? value() : value).then(setDone);
  };
  return text ? (
    <Button variant="secondary" icon={icon} className={className} data-copied={done || undefined} onClick={onClick}>{name}</Button>
  ) : (
    <IconButton label={name} icon={icon} size={size} className={className} data-copied={done || undefined} onClick={onClick} />
  );
}
