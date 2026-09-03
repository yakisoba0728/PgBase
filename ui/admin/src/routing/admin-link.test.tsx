import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AdminLink } from "./admin-link";
import { adminRoutePath, parseAdminRoute, type AdminRoute } from "./admin-location";

const routes: AdminRoute[] = [
  { kind: "collections" },
  { kind: "collection-create" },
  { kind: "system" },
  { kind: "collection-edit", collection: "a b" },
  { kind: "records", collection: "posts" },
  { kind: "record", collection: "posts" },
  { kind: "record", collection: "posts", id: "one two" },
];

describe("administration links", () => {
  it.each(routes)("addresses $kind with a href that parses back to the route", (route) => {
    const navigate = vi.fn();
    render(<AdminLink navigate={navigate} route={route}>go</AdminLink>);

    const href = screen.getByRole("link", { name: "go" }).getAttribute("href");
    expect(href).toBe(adminRoutePath(route));
    expect(parseAdminRoute(href ?? "")).toEqual(route);
  });

  it("navigates in place on a plain click instead of reloading the document", () => {
    const navigate = vi.fn();
    render(<AdminLink navigate={navigate} route={{ kind: "records", collection: "posts" }}>posts</AdminLink>);

    const click = fireEvent.click(screen.getByRole("link", { name: "posts" }), { button: 0 });

    expect(click).toBe(false);
    expect(navigate).toHaveBeenCalledWith({ kind: "records", collection: "posts" });
  });

  it.each([
    ["ctrlKey" as const],
    ["metaKey" as const],
    ["shiftKey" as const],
    ["altKey" as const],
  ])("leaves a %s click to the browser so it can open its own tab or window", (modifier) => {
    const navigate = vi.fn();
    render(<AdminLink navigate={navigate} route={{ kind: "system" }}>System</AdminLink>);

    const click = fireEvent.click(screen.getByRole("link", { name: "System" }), { button: 0, [modifier]: true });

    expect(click).toBe(true);
    expect(navigate).not.toHaveBeenCalled();
  });
});
