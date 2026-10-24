import type { MouseEvent, ReactNode } from "react";
import { adminRoutePath, type AdminRoute } from "./admin-location";

// A modified or non-primary click belongs to the browser. Letting it through is
// what makes a new tab, a new window and "copy link address" work, and the href
// comes from the route formatter so every one of them lands on a typed route.
function browserOwns(event: MouseEvent<HTMLAnchorElement>): boolean {
  return event.button !== 0 || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey;
}

export function AdminLink({ children, className, navigate, route, current, ariaLabel }: {
  children: ReactNode;
  className?: string;
  current?: boolean;
  ariaLabel?: string;
  navigate(route: AdminRoute): void;
  route: AdminRoute;
}) {
  return <a
    aria-label={ariaLabel}
    aria-current={current ? "page" : undefined}
    className={className}
    href={adminRoutePath(route)}
    onClick={(event) => {
      if (event.defaultPrevented || browserOwns(event)) return;
      event.preventDefault();
      navigate(route);
    }}
  >{children}</a>;
}
