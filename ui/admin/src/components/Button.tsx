import type { ButtonHTMLAttributes, ComponentProps } from "react";
import type { RemixiconComponentType } from "@remixicon/react";

interface ButtonProps extends ComponentProps<"button"> {
  variant?: "primary" | "secondary" | "transparent" | "danger";
  size?: "sm";
  block?: boolean;
  loading?: boolean;
  icon?: RemixiconComponentType;
}

export function Button({ variant = "primary", size, block, loading, icon: Icon, className, children, disabled, type = "button", ...rest }: ButtonProps) {
  const classes = ["btn", variant !== "primary" && `btn-${variant}`, size && `btn-${size}`, block && "btn-block", className];
  return (
    <button type={type} className={classes.filter(Boolean).join(" ")} disabled={disabled || loading} aria-busy={loading || undefined} {...rest}>
      {loading ? <span className="loader" aria-hidden /> : Icon && <Icon size={16} aria-hidden />}
      {children}
    </button>
  );
}

interface IconButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> {
  label: string;
  icon: RemixiconComponentType;
}

export function IconButton({ label, icon: Icon, className, type = "button", ...rest }: IconButtonProps) {
  return (
    <button type={type} className={className ? `icon-btn ${className}` : "icon-btn"} aria-label={label} title={label} {...rest}>
      <Icon size={16} aria-hidden />
    </button>
  );
}
