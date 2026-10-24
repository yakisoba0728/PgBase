import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { WorkspaceContextNavigation, WorkspaceNavigation } from "./components/workspace-chrome";
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
  it("keeps only five primary tasks in the sidebar and finds secondary screens on demand", async () => {
    configuredChrome({ kind: "collections" });
    const user = userEvent.setup();
    const nav = within(screen.getByRole("navigation", { name: "Administration" }));
    expect(nav.getAllByRole("link").map(link => link.textContent)).toEqual(["Collections", "File storage", "Users", "System", "Settings"]);
    await user.type(screen.getByRole("searchbox", { name: "Find a page" }), "token");
    expect(nav.getAllByRole("link")).toHaveLength(1);
    expect(nav.getByRole("link", { name: "Service tokens" })).toHaveAttribute("href", "/admin/service-tokens");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(nav.getAllByRole("link")).toHaveLength(5);
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
    expect(nav.queryByRole("link", { name: label })).toBeNull();
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
    expect(nav.getAllByRole("link")).toHaveLength(4);
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
