import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  adminRouteIdentity,
  adminRoutePath,
  browserHistory,
  canonicalAdminPath,
  parseAdminRoute,
  useAdminLocation,
  useResourceState,
  useRouteGuard,
  type AdminRoute,
} from "./admin-location";
import { memoryHistory } from "../test/memory-history";
import { draftLedger } from "./unsaved-draft";

const routes: AdminRoute[] = [
  { kind: "collections" },
  { kind: "collection-create" },
  { kind: "collection-duplicate", collection: "posts" },
  { kind: "collection-scaffold", scaffold: "base" },
  { kind: "collection-scaffold", scaffold: "auth" },
  { kind: "collection-scaffold", scaffold: "view" },
  { kind: "extension", id: "reports", path: "/admin/ext/reports" },
  { kind: "operations" },
	{ kind: "sql-console" },
  { kind: "system" },
  { kind: "collection-edit", collection: "posts" },
  { kind: "records", collection: "posts" },
  { kind: "record", collection: "posts" },
  { kind: "record", collection: "posts", id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" },
];

afterEach(() => window.history.replaceState(null, "", "/admin/"));

describe("admin routes", () => {
  it.each(routes)("round-trips $kind through its path", (route) => {
    expect(parseAdminRoute(adminRoutePath(route))).toEqual(route);
  });

  it.each(["new", "New", "NEW", "edit", "duplicate", "records", "MixedCase01"])("keeps text record ID %s distinct from the create route", (id) => {
    const route: AdminRoute = { kind: "record", collection: "posts", id };
    const path = adminRoutePath(route);
    expect(path).not.toBe(adminRoutePath({ kind: "record", collection: "posts" }));
    expect(parseAdminRoute(path)).toEqual(route);
    browserHistory.push(path);
    expect(parseAdminRoute(browserHistory.path())).toEqual(route);
  });

  it.each([
    ["/admin/collections/new", { kind: "collection-create" }],
    ["/admin/collections/new/base", { kind: "collection-scaffold", scaffold: "base" }],
    ["/admin/collections/posts/duplicate", { kind: "collection-duplicate", collection: "posts" }],
    ["/admin/ext/reports", { kind: "extension", id: "reports", path: "/admin/ext/reports" }],
    ["/admin/schema-operations", { kind: "operations" }],
		["/admin/sql", { kind: "sql-console" }],
    ["/admin/system", { kind: "system" }],
    ["/admin/collections/posts/edit", { kind: "collection-edit", collection: "posts" }],
    ["/admin/collections/posts/records", { kind: "records", collection: "posts" }],
    ["/admin/collections/posts/records/new", { kind: "record", collection: "posts" }],
    ["/admin/collections/new/records", { kind: "records", collection: "new" }],
    ["/admin/collections/a%20b/edit", { kind: "collection-edit", collection: "a b" }],
    ["/admin/", { kind: "collections" }],
    ["/admin/nonsense", { kind: "collections" }],
  ])("parses %s", (path, route) => {
    expect(parseAdminRoute(path)).toEqual(route);
  });

  it.each(["/admin/collections/%E0%A4%A/records", "/admin/collections/%E0%A4%A/edit", "/admin/collections/posts/records/%E0%A4%A"])(
    "falls back to the collection list for the undecodable path %s",
    (path) => expect(parseAdminRoute(path)).toEqual({ kind: "collections" }),
  );

  it("keeps two resources apart even when one name contains the other's separator", () => {
    const identities = [
      ...routes.map(adminRouteIdentity),
      adminRouteIdentity({ kind: "records", collection: "posts records" }),
      adminRouteIdentity({ kind: "record", collection: "posts", id: "new" }),
      adminRouteIdentity({ kind: "collection-edit", collection: "records posts" }),
    ];
    expect(new Set(identities).size).toBe(identities.length);
  });

  it.each([
    ["setup" as const, "/admin/collections/posts/records", "/admin/setup"],
    ["login" as const, "/admin/collections", "/admin/login"],
    ["shell" as const, "/admin/collections/posts/edit", "/admin/collections/posts/edit"],
    ["shell" as const, "/admin/login", "/admin/collections"],
    ["shell" as const, "/admin/setup", "/admin/collections"],
    ["shell" as const, "/", "/admin/collections"],
  ])("canonicalises the %s screen at %s", (screen, path, expected) => {
    expect(canonicalAdminPath(screen, path)).toBe(expected);
  });
});

describe("history adapters", () => {
  it.each([
    ["memory", () => memoryHistory("/admin/collections")],
    ["browser", () => { window.history.replaceState(null, "", "/admin/collections"); return browserHistory; }],
  ])("announces a push and a replace through the %s adapter", (_name, open) => {
    const history = open();
    const changes = vi.fn();
    const stop = history.listen(changes);

    history.push("/admin/system");
    expect(history.path()).toBe("/admin/system");
    history.replace("/admin/collections/posts/records");
    expect(history.path()).toBe("/admin/collections/posts/records");
    expect(changes).toHaveBeenCalledTimes(2);

    stop();
    history.push("/admin/collections");
    expect(changes).toHaveBeenCalledTimes(2);
  });

  it.each([
    ["memory", () => { const history = memoryHistory("/admin/collections"); return { history, move: () => history.back() }; }],
    ["browser", () => {
      window.history.replaceState(null, "", "/admin/collections");
      return { history: browserHistory, move: () => window.dispatchEvent(new PopStateEvent("popstate")) };
    }],
  ])("tells a browser move apart from its own address changes through the %s adapter", (_name, open) => {
    const { history, move } = open();
    const changes = vi.fn();
    const stop = history.listen(changes);

    history.push("/admin/system");
    expect(changes).toHaveBeenLastCalledWith(false);
    history.replace("/admin/collections");
    expect(changes).toHaveBeenLastCalledWith(false);
    move();
    expect(changes).toHaveBeenLastCalledWith(true);

    stop();
  });

  it("walks back and forward through memory entries", () => {
    const history = memoryHistory("/admin/collections");
    const { result } = renderHook(() => useAdminLocation(history));

    act(() => history.push("/admin/system"));
    expect(result.current.route).toEqual({ kind: "system" });
    act(() => history.back());
    expect(result.current.route).toEqual({ kind: "collections" });
    act(() => history.forward());
    expect(result.current.route).toEqual({ kind: "system" });
  });

  it("navigates by typed route rather than by path", () => {
    const history = memoryHistory("/admin/collections");
    const { result } = renderHook(() => useAdminLocation(history));

    act(() => result.current.navigate({ kind: "record", collection: "a b", id: "one two" }));
    expect(history.path()).toBe("/admin/collections/a%20b/records/one%20two");
    expect(result.current.identity).toBe(adminRouteIdentity({ kind: "record", collection: "a b", id: "one two" }));
  });
});

describe("route departure", () => {
  it.each(["posts", "%70osts"])("keeps the draft guard when typed navigation stays on %s", (collectionPath) => {
    const history = memoryHistory(`/admin/collections/${collectionPath}/records/one`);
    const confirmLeave = vi.fn(() => true);
    const ledger = draftLedger(confirmLeave);
    ledger.hold("record posts id:one", true);
    const { result } = renderHook(() => useAdminLocation(history, ledger.mayLeave));

    act(() => result.current.navigate({ kind: "record", collection: "posts", id: "one" }));
    expect(confirmLeave).not.toHaveBeenCalled();
    expect(ledger.held()).toBe(true);
    expect(history.entries).toHaveLength(1);

    confirmLeave.mockReturnValue(false);
    act(() => result.current.navigate({ kind: "collections" }));
    expect(history.path()).toBe(`/admin/collections/${collectionPath}/records/one`);
    expect(ledger.held()).toBe(true);
  });

  it("keeps the draft guard when browser history changes only a resource's encoding", () => {
    const history = memoryHistory("/admin/collections/%70osts/records/one");
    const confirmLeave = vi.fn(() => true);
    const ledger = draftLedger(confirmLeave);
    renderHook(() => useAdminLocation(history, ledger.mayLeave));
    act(() => history.push("/admin/collections/posts/records/one"));
    ledger.hold("record posts id:one", true);

    act(() => history.back());
    expect(confirmLeave).not.toHaveBeenCalled();
    expect(ledger.held()).toBe(true);
    expect(history.path()).toBe("/admin/collections/%70osts/records/one");
  });

  it("restores the address the browser moved away from when the departure is refused", () => {
    const history = memoryHistory("/admin/collections");
    const { result } = renderHook(() => useAdminLocation(history, () => false));

    act(() => history.push("/admin/collections/posts/records"));
    expect(result.current.route).toEqual({ kind: "records", collection: "posts" });

    act(() => history.back());
    expect(history.path()).toBe("/admin/collections/posts/records");
    expect(result.current.route).toEqual({ kind: "records", collection: "posts" });
  });

  it("moves on once the departure is allowed", () => {
    const history = memoryHistory("/admin/collections");
    let allowed = false;
    const { result } = renderHook(() => useAdminLocation(history, () => allowed));

    act(() => history.push("/admin/system"));
    act(() => history.back());
    expect(result.current.route).toEqual({ kind: "system" });

    allowed = true;
    act(() => history.back());
    expect(result.current.route).toEqual({ kind: "collections" });
  });

  it("refuses a typed navigation but never the address this application sets itself", () => {
    const history = memoryHistory("/admin/collections");
    const { result } = renderHook(() => useAdminLocation(history, () => false));

    act(() => result.current.navigate({ kind: "system" }));
    expect(history.path()).toBe("/admin/collections");

    // The session canonicalises the address on its own; a forced sign-out is
    // not a departure the administrator is asked about.
    act(() => history.replace("/admin/system"));
    expect(result.current.route).toEqual({ kind: "system" });
  });
});

describe("late results", () => {
  it("goes false once the outlet is reused for another identity or unmounted", () => {
    const { result, rerender, unmount } = renderHook(({ identity }) => useRouteGuard(identity), { initialProps: { identity: "records posts" } });
    const captured = result.current;
    expect(captured()).toBe(true);

    rerender({ identity: "records users" });
    expect(captured()).toBe(false);
    expect(result.current()).toBe(true);

    const stillCurrent = result.current;
    unmount();
    expect(stillCurrent()).toBe(false);
  });

  it("rebuilds resource state during the render that changes identity", () => {
    const { result, rerender } = renderHook(
      ({ identity }) => useResourceState(identity, (current) => ({ identity: current, filter: "" })),
      { initialProps: { identity: "records posts" } },
    );
    const issuedForPosts = result.current[1];
    act(() => issuedForPosts("records posts", { filter: "title = 'x'" }));
    expect(result.current[0].filter).toBe("title = 'x'");

    rerender({ identity: "records users" });
    expect(result.current[0]).toEqual({ identity: "records users", filter: "" });

    act(() => issuedForPosts("records posts", { filter: "late result" }));
    expect(result.current[0].filter).toBe("");
  });
});
