import { CopyButton } from "./CopyButton";
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
  const parts = typeof text === "string" ? [{ text }] : text;
  return (
    <div className="code-block" data-wrap={wrap ? "" : undefined}>
      <div className="code-block-body" tabIndex={0} role="region" aria-label={label}>
        {parts.map((part, position) => (
          <pre key={position} className="mono" data-danger={part.danger ? "" : undefined}>{part.text}</pre>
        ))}
      </div>
      <CopyButton className="code-block-copy" value={() => parts.map((part) => part.text).join(typeof text === "string" ? "" : ";\n\n")} />
    </div>
  );
}
