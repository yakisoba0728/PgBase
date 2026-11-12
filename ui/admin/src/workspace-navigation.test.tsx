import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { WorkspaceContextNavigation, WorkspaceNavigation } from "./components/workspace-chrome";
import { CollectionCatalog } from "./backend/ports";
import type { AdminRoute } from "./routing/admin-location";
import { renderAdmin, scriptedAdmin, sessionWrapper } from "./test/harness";

function configuredChrome(route: AdminRoute) {
  const h = scriptedAdmin();
  h.backend.settings = { get: vi.fn(), save: vi.fn(), reload: vi.fn() };
  h.backend.administrators = { list: vi.fn(), create: vi.fn(), setActive: vi.fn(), resetPassword: vi.fn(), revokeSessions: vi.fn() };
  h.backend.schema.exportDocument = vi.fn();
  h.backend.schema.previewImport = vi.fn();
  h.backend.schema.applyImport = vi.fn();
  const navigate = vi.fn();
  render(<><WorkspaceNavigation route={route} open={false} navigate={navigate} onClose={vi.fn()} extensions={[]} sqlEnabled={false} /><WorkspaceContextNavigation route={route} navigate={navigate} /></>, { wrapper: sessionWrapper(h.backend, h.history) });
  return { h, navigate };
}

describe("focused workspace navigation", () => {
  it("keeps collection navigation focused and finds secondary screens on demand", async () => {
    configuredChrome({ kind: "collections" });
    const user = userEvent.setup();
    const nav = within(screen.getByRole("navigation", { name: "Administration" }));
    expect(nav.getAllByRole("link").map(link => link.textContent)).toEqual(["Settings"]);
    await user.type(screen.getByRole("searchbox", { name: "Find a page" }), "token");
    expect(nav.getAllByRole("link")).toHaveLength(1);
    expect(nav.getByRole("link", { name: "Service tokens" })).toHaveAttribute("href", "/admin/service-tokens");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(nav.getAllByRole("link")).toHaveLength(1);
    expect(nav.queryByRole("link", { name: "Service tokens" })).toBeNull();
  });

  it.each([
    [{ kind: "schema-transfer" }, "Collections", "Schema transfer", "/admin/schema-transfer"],
    [{ kind: "backup-storage" }, "File storage", "Backup storage", "/admin/storage/backups"],
    [{ kind: "auth-collections" }, "Users", "Auth collections", "/admin/auth-collections"],
    [{ kind: "administrators" }, "Users", "Administrators", "/admin/administrators"],
    [{ kind: "service-tokens" }, "Users", "Service tokens", "/admin/service-tokens"],
    [{ kind: "operations" }, "System", "Schema operations", "/admin/schema-operations"],
  ] as const)("retains a typed route and parent context for %s", async (route, parent, label, path) => {
    const { navigate } = configuredChrome(route);
    const nav = within(screen.getByRole("navigation", { name: "Administration" }));
    expect(nav.getByRole("link", { name: parent })).toHaveAttribute("aria-current", "page");
    expect(nav.getByRole("link", { name: label })).toHaveAttribute("href", path);
    const context = within(screen.getByRole("navigation", { name: `${parent} workspace` }));
    const link = context.getByRole("link", { name: label });
    expect(link).toHaveAttribute("href", path);
    expect(link).toHaveAttribute("aria-current", "page");
    await userEvent.setup().click(link);
    expect(navigate).toHaveBeenCalledWith(route);
  });

  it("omits unavailable settings, administrators and incomplete schema transfer from navigation and search", async () => {
    const h = scriptedAdmin();
    h.backend.schema.exportDocument = vi.fn();
    render(<><WorkspaceNavigation route={{ kind: "users" }} open={false} navigate={vi.fn()} onClose={vi.fn()} extensions={[]} sqlEnabled={false} /><WorkspaceContextNavigation route={{ kind: "users" }} navigate={vi.fn()} /></>, { wrapper: sessionWrapper(h.backend, h.history) });
    const nav = within(screen.getByRole("navigation", { name: "Administration" }));
    expect(nav.getByRole("link", { name: "Users" })).toBeVisible();
    expect(screen.queryByRole("link", { name: "Settings" })).toBeNull();
    expect(screen.queryByRole("link", { name: "Administrators" })).toBeNull();
    await userEvent.setup().type(screen.getByRole("searchbox", { name: "Find a page" }), "administrator");
    expect(nav.queryAllByRole("link")).toHaveLength(0);
    expect(screen.getByRole("status")).toHaveTextContent("0 pages");
    await userEvent.setup().clear(screen.getByRole("searchbox", { name: "Find a page" }));
    await userEvent.setup().type(screen.getByRole("searchbox", { name: "Find a page" }), "import");
    expect(nav.queryAllByRole("link")).toHaveLength(0);
  });

  it("keeps an unsaved backup draft when departure through the new storage tab is declined", async () => {
    const h = scriptedAdmin("/admin/storage/backups");
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(h);
    const retention = await screen.findByLabelText("Backups to retain");
    await user.clear(retention);
    const context = within(screen.getByRole("navigation", { name: "File storage workspace" }));
    await user.click(context.getByRole("link", { name: "Open file storage section" }));
    expect(h.history.path()).toBe("/admin/storage/backups");
    expect(retention).toHaveValue(null);
    expect(confirm).toHaveBeenCalledOnce();
  });
});


it("lists real collections, filters them, and opens their record routes", async () => {
  const h = scriptedAdmin();
  h.backend.schema.catalog = vi.fn(async () => new CollectionCatalog(4, [
    { id: "posts", name: "posts", tableName: "posts", fields: [] },
    { id: "members", name: "members", tableName: "members", type: "auth", fields: [] },
  ]));
  const navigate = vi.fn();
  render(<WorkspaceNavigation route={{ kind: "records", collection: "posts" }} open={false} navigate={navigate} onClose={vi.fn()} extensions={[]} sqlEnabled={false} />, { wrapper: sessionWrapper(h.backend, h.history) });
  const link = await screen.findByRole("link", { name: "Open posts collection" });
  expect(link).toHaveAttribute("aria-current", "page");
  await userEvent.click(link);
  expect(navigate).toHaveBeenCalledWith({ kind: "records", collection: "posts" });
  await userEvent.type(screen.getByRole("searchbox", { name: "Find a page" }), "member");
  expect(screen.queryByRole("link", { name: "Open posts collection" })).toBeNull();
  expect(screen.getByRole("link", { name: "Open members collection" })).toHaveAttribute("href", "/admin/collections/members/records");
  expect(screen.getByRole("link", { name: "New collection" })).toHaveAttribute("href", "/admin/collections/new");
});

it("retries failed collection navigation without hiding the other pages", async () => {
  const h = scriptedAdmin();
  h.backend.schema.catalog = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(new CollectionCatalog(1, []));
  render(<WorkspaceNavigation route={{ kind: "collections" }} open={false} navigate={vi.fn()} onClose={vi.fn()} extensions={[]} sqlEnabled={false} />, { wrapper: sessionWrapper(h.backend, h.history) });
  await userEvent.click(await screen.findByRole("button", { name: "Retry collection navigation" }));
  expect(await screen.findByText("Create a collection to get started.")).toBeVisible();
  expect(screen.getByRole("link", { name: "System" })).toBeVisible();
});

it("links directly to logs and respects server capabilities", () => {
  const h = scriptedAdmin(); h.backend.capabilities = { logs: false };
  render(<WorkspaceNavigation route={{ kind: "collections" }} open={false} navigate={vi.fn()} onClose={vi.fn()} extensions={[]} sqlEnabled={false} />, { wrapper: sessionWrapper(h.backend, h.history) });
  expect(screen.queryByRole("link", { name: "Logs section" })).toBeNull();
});


it("ignores an old catalog response after navigation starts a newer read", async () => {
  const h = scriptedAdmin();
  let oldRead!: (catalog: CollectionCatalog) => void;
  h.backend.schema.catalog = vi.fn().mockImplementationOnce(() => new Promise<CollectionCatalog>(resolve => { oldRead = resolve; })).mockResolvedValueOnce(new CollectionCatalog(2, [{ name: "current", tableName: "current", fields: [] }]));
  const chrome = (route: AdminRoute) => <WorkspaceNavigation route={route} open={false} navigate={vi.fn()} onClose={vi.fn()} extensions={[]} sqlEnabled={false} />;
  const view = render(chrome({ kind: "collections" }), { wrapper: sessionWrapper(h.backend, h.history) });
  view.rerender(chrome({ kind: "records", collection: "current" }));
  expect(await screen.findByRole("link", { name: "Open current collection" })).toBeVisible();
  await act(async () => oldRead(new CollectionCatalog(1, [{ name: "obsolete", tableName: "obsolete", fields: [] }])));
  expect(screen.queryByRole("link", { name: "Open obsolete collection" })).toBeNull();
  expect(screen.getByRole("link", { name: "Open current collection" })).toBeVisible();
});
