import type { ButtonHTMLAttributes, ChangeEvent, ComponentProps, ReactNode } from "react";
import { useQueryClient, type QueryKey } from "@tanstack/react-query";
import { RiRefreshLine, type RemixiconComponentType } from "@remixicon/react";
import { useT } from "../i18n";

interface ButtonProps extends ComponentProps<"button"> {
  variant?: "primary" | "secondary" | "transparent" | "danger";
  size?: "sm";
  block?: boolean;
  loading?: boolean;
  icon?: RemixiconComponentType;
}

const buttonClass = ({ variant = "primary", size, block, className }: Pick<ButtonProps, "variant" | "size" | "block" | "className">) =>
  ["btn", variant !== "primary" && `btn-${variant}`, size && `btn-${size}`, block && "btn-block", className].filter(Boolean).join(" ");

export function Button({ variant = "primary", size, block, loading, icon: Icon, className, children, disabled, type = "button", ...rest }: ButtonProps) {
  return (
    <button type={type} className={buttonClass({ variant, size, block, className })} disabled={disabled || loading} aria-busy={loading || undefined} {...rest}>
      {loading ? <span className="loader" aria-hidden /> : Icon && <Icon size={16} aria-hidden />}
      {children}
    </button>
  );
}

interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  label: string;
  icon: RemixiconComponentType;
  /** 30px by default (--control-sm); "xs" is 24px, for buttons inside a control, a cell, a list row or a heading. */
  size?: "xs";
}

export function IconButton({ label, icon: Icon, size, className, type = "button", ...rest }: IconButtonProps) {
  return (
    <button type={type} className={["icon-btn", size && `icon-btn-${size}`, className].filter(Boolean).join(" ")} aria-label={label} title={label} {...rest}>
      <Icon size={16} aria-hidden />
    </button>
  );
}

/** The page-header refresh: refetches a screen's data; `reset` drops loaded pages first (long cursor lists). */
export function RefreshButton({ queryKey, reset }: { queryKey: QueryKey; reset?: boolean }) {
  const t = useT();
  const queryClient = useQueryClient();
  return <IconButton label={t("common.refresh")} icon={RiRefreshLine} onClick={() => void (reset ? queryClient.resetQueries({ queryKey }) : queryClient.invalidateQueries({ queryKey }))} />;
}

type FileButtonProps = Omit<ComponentProps<"input">, "type" | "className" | "size" | "onChange" | "children" | "title"> & {
  /** "link" for the quiet inline look (a file list's "Upload"); a button otherwise. */
  look?: "link";
  variant?: ButtonProps["variant"];
  size?: "sm";
  icon?: RemixiconComponentType;
  title?: string;
  children: ReactNode;
  /** The picked files; the input is cleared first so the same file can be picked again. */
  onFiles: (files: File[]) => void;
};

/** The one file picker: a label styled as a button (or link) around a visually hidden file input, so keyboard, focus and `getByLabel` reach the input. */
export function FileButton({ look, variant = "secondary", size, icon: Icon, title, children, onFiles, ...input }: FileButtonProps) {
  const onChange = (event: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    onFiles(files);
  };
  return (
    <label className={look === "link" ? "link file-button" : buttonClass({ variant, size, className: "file-button" })} title={title}>
      {Icon && <Icon size={look === "link" ? 14 : 16} aria-hidden />}
      {children}
      <input type="file" className="visually-hidden" onChange={onChange} {...input} />
    </label>
  );
}
