import type { ReactNode } from "react";
import { NavLink } from "react-router";
import type { RemixiconComponentType } from "@remixicon/react";

export function NavGroup({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <nav className="nav-group" aria-label={title}>
      <div className="nav-group-title">
        <span>{title}</span>
        {action}
      </div>
      {children}
    </nav>
  );
}

interface NavItemProps {
  to: string;
  icon: RemixiconComponentType;
  className?: string;
  children: ReactNode;
}

/** The label doubles as the tooltip so the icon rail stays readable. */
export function NavItem({ to, icon: Icon, className, children }: NavItemProps) {
  return (
    <NavLink to={to} className={className ? `nav-link ${className}` : "nav-link"} title={typeof children === "string" ? children : undefined}>
      <Icon size={16} aria-hidden />
      <span className="nav-label">{children}</span>
    </NavLink>
  );
}

interface RunningOperation {
  id: string;
  label: string;
  /** Short progress text such as "42%". */
  progress?: string;
}

/** Slim list of long-running work (schema apply, file migration, backup); hidden when idle. */
export function RunningOperations({ label, items }: { label: string; items: RunningOperation[] }) {
  if (items.length === 0) return null;
  return (
    <section className="nav-ops" aria-label={label} aria-live="polite">
      {items.map((item) => (
        <div key={item.id} className="nav-op" title={item.label}>
          <span className="loader" aria-hidden />
          <span className="nav-label">{item.label}</span>
          {item.progress && <span>{item.progress}</span>}
        </div>
      ))}
    </section>
  );
}
