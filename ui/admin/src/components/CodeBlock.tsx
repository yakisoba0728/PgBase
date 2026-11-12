import { RiFileCopyLine } from "@remixicon/react";
import { useT } from "../i18n";
import { IconButton } from "./Button";
import { toast } from "./Toast";
import "../styles/widgets.css";

interface CodeBlockProps {
  /** One text, or several statements where some are marked dangerous. */
  text: string | { text: string; danger?: boolean }[];
  /** Accessible name of the scroll area. */
  label: string;
  /** Wraps long lines (SQL); JSON keeps its layout and scrolls sideways. */
  wrap?: boolean;
}

/** Read-only code or JSON in a bounded scroll area, with a copy button. */
export function CodeBlock({ text, label, wrap }: CodeBlockProps) {
  const t = useT();
  const parts = typeof text === "string" ? [{ text }] : text;
  const copy = () =>
    navigator.clipboard.writeText(parts.map((part) => part.text).join(typeof text === "string" ? "" : ";\n\n")).then(
      () => toast(t("records.copied")),
      () => toast(t("records.copyFailed"), "danger"),
    );
  return (
    <div className="code-block" data-wrap={wrap ? "" : undefined}>
      <div className="code-block-body" tabIndex={0} role="region" aria-label={label}>
        {parts.map((part, position) => (
          <pre key={position} className="mono" data-danger={part.danger ? "" : undefined}>{part.text}</pre>
        ))}
      </div>
      <IconButton className="code-block-copy" label={t("devtools.copy")} icon={RiFileCopyLine} onClick={copy} />
    </div>
  );
}
