import type { KeyboardEvent, TextareaHTMLAttributes } from "react";
import "../styles/widgets.css";

type CodeAreaProps = Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "className" | "onKeyDown"> & {
  label: string;
  /** Cmd/Ctrl+Enter. */
  onSubmit?: () => void;
  /** Warning tone for text that will write (SQL write mode). */
  tone?: "warning";
  /** Fills the parent's height instead of sizing by rows. */
  fill?: boolean;
};

/** A monospace editor for SQL and JSON: no spellcheck or autocorrect, Cmd/Ctrl+Enter submits. Tab keeps moving focus. */
export function CodeArea({ label, onSubmit, tone, fill, ...rest }: CodeAreaProps) {
  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey) && onSubmit) {
      event.preventDefault();
      onSubmit();
    }
  };
  return (
    <textarea
      className="code-area mono"
      data-tone={tone}
      data-fill={fill ? "" : undefined}
      aria-label={label}
      spellCheck={false}
      autoComplete="off"
      autoCapitalize="off"
      onKeyDown={onKeyDown}
      {...rest}
    />
  );
}
