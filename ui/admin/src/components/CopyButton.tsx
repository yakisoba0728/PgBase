import type { MouseEvent } from "react";
import { RiFileCopyLine } from "@remixicon/react";
import { useT } from "../i18n";
import { IconButton } from "./Button";
import { toast } from "./Toast";

/** Copies text to the clipboard (a function, so large values are only built on click). */
export function copyText(text: string, done: string, failed: string): void {
  navigator.clipboard.writeText(text).then(
    () => toast(done),
    () => toast(failed, "danger"),
  );
}

export function CopyButton({ label, value, className }: { label: string; value: string | (() => string); className?: string }) {
  const t = useT();
  const onClick = (event: MouseEvent) => {
    event.stopPropagation();
    copyText(typeof value === "function" ? value() : value, t("copy.done"), t("copy.failed"));
  };
  return <IconButton label={label} icon={RiFileCopyLine} className={className} onClick={onClick} />;
}
