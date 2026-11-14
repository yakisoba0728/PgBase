import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { expect, it } from "vitest";
import { PageSkeleton } from "./PageSkeleton";
import { Skeleton } from "./Feedback";

it.each([
  ["/collections", "table"], ["/collections/student", "table"],
  ["/collections/student/schema", "form"], ["/collections/student/rules", "form"],
  ["/collections/student/api", "editor"], ["/new-collection/base", "form"],
  ["/logs", "table"], ["/jobs", "table"], ["/realtime", "cards"],
  ["/auth/collections", "table"], ["/auth/collections/users", "form"], ["/auth/users", "table"],
  ["/backups", "table"], ["/storage", "form"], ["/storage/migration", "form"],
  ["/settings", "form"], ["/settings/status", "cards"], ["/service-tokens", "table"],
  ["/administrators", "table"], ["/profile", "form"], ["/schema-transfer", "form"],
  ["/schema-operations", "table"], ["/sql", "editor"],
])("keeps the destination heading and %s loading layout", (path, variant) => {
  const { container } = render(<MemoryRouter initialEntries={[path]}><PageSkeleton /></MemoryRouter>);
  expect(screen.getByRole("heading", { level: 1 })).not.toBeEmptyDOMElement();
  expect(screen.getByRole("button", { name: "Open navigation" })).toBeEnabled();
  expect(container.querySelector(`.skeleton-${variant}`)).toHaveAttribute("aria-busy", "true");
  expect(container.querySelector(".loader")).toBeNull();
  expect(screen.getAllByRole("status")).toHaveLength(1);
});

it("keeps collection tabs and decoded names while loading", () => {
  render(<MemoryRouter initialEntries={["/collections/%ED%95%99%EC%83%9D/schema"]}><PageSkeleton /></MemoryRouter>);
  expect(screen.getByRole("heading", { name: "학생" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Schema" })).toHaveAttribute("aria-current", "page");
});

it.each(["table", "form", "cards", "editor", "list"] as const)("announces %s loading without fake focusable controls", (variant) => {
  const { container } = render(<Skeleton variant={variant} />);
  expect(screen.getByRole("status")).toHaveTextContent("Loading");
  expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
  expect(container.querySelector(".skeleton-content")).toHaveAttribute("aria-hidden", "true");
  expect(container.querySelectorAll("input, button, a, [tabindex]")).toHaveLength(0);
});
