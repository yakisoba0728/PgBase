import type { ButtonHTMLAttributes, FieldsetHTMLAttributes, ReactNode } from "react";
import { WorkspaceIcon, type WorkspaceIconName } from "./workspace-icon";
import { AdminLink } from "../routing/admin-link";
import type { AdminRoute } from "../routing/admin-location";

export function Button({ variant = "primary", className = "", type = "button", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "ghost" | "danger" }) {
  return <button {...props} type={type} className={`${variant === "ghost" ? "text-button" : variant} ${className}`} />;
}
export function PageHeading({ title, description, actions }: { title: ReactNode; description?: ReactNode; actions?: ReactNode }) {
  return <div className="page-heading"><div><h2>{title}</h2>{description && <p className="page-description">{description}</p>}</div>{actions}</div>;
}
export function FormSection({ title, className = "", children, ...props }: Omit<FieldsetHTMLAttributes<HTMLFieldSetElement>, "title"> & { title: ReactNode }) {
  return <fieldset {...props} className={`form-section ${className}`}><legend>{title}</legend>{children}</fieldset>;
}
export function ActionBar({ children, description, sticky = false }: { children: ReactNode; description?: ReactNode; sticky?: boolean }) {
  return <div className={`page-actions${sticky ? " is-sticky" : ""}`}>{description && <p>{description}</p>}<div className="page-action-buttons">{children}</div></div>;
}
export function BackLink({ children, route, navigate }: { children: ReactNode; route: AdminRoute; navigate(route: AdminRoute): void }) {
  return <AdminLink className="back-link" route={route} navigate={navigate}><WorkspaceIcon name="arrow" />{children}</AdminLink>;
}
export function EmptyState({ title, description, action, icon }: { title: string; description?: ReactNode; action?: ReactNode; icon?: WorkspaceIconName }) {
  return <div className="collection-empty">{icon && <span className="empty-illustration"><WorkspaceIcon name={icon} /></span>}<h3>{title}</h3>{description && <p>{description}</p>}{action}</div>;
}
