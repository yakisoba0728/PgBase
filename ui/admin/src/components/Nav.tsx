import type { ReactNode } from "react";
import { Link, NavLink } from "react-router";
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
  /** The screen that shows the operation. */
  href?: string;
  /** Stalled until someone acts (no spinner, warning colour). */
  attention?: boolean;
}

/** Slim list of long-running work (schema apply, file migration, backup); hidden when idle. */
export function RunningOperations({ label, items }: { label: string; items: RunningOperation[] }) {
  if (items.length === 0) return null;
  return (
    <section className="nav-ops" aria-label={label} aria-live="polite">
      {items.map((item) => {
        const content = (
          <>
            {item.attention ? <span className="dot" data-tone="warning" aria-hidden /> : <span className="loader" aria-hidden />}
            <span className="nav-label">{item.label}</span>
            {item.progress && <span className="nav-op-progress">{item.progress}</span>}
          </>
        );
        return item.href ? (
          <Link key={item.id} to={item.href} className="nav-op" title={item.label}>{content}</Link>
        ) : (
          <div key={item.id} className="nav-op" title={item.label}>{content}</div>
        );
      })}
    </section>
  );
}
