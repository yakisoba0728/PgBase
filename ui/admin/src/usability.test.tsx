import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { AdminSettings } from "./backend/ports";
import { collection, renderAdmin, scriptedAdmin, textField } from "./test/harness";

const settings: AdminSettings = {
  batch: { enabled: true, maxRequests: 10, maxBodyBytes: 4096, timeoutMs: 1000 },
  logs: { retentionDays: 7, level: "info", includeIP: false, includeAuth: false },
  proxy: { trustedProxyCIDRs: [] }, backups: { enabled: false, keep: 0, schedule: "@every 24h", timezone: "UTC" },
  application: { theme: "system", accent: "", branding: "pgBase" },
  rateLimits: { admin: { accountMaxFailures: 5, networkMaxFailures: 20, windowSeconds: 900, blockSeconds: 900 }, user: { accountMaxFailures: 5, networkMaxFailures: 20, registrationNetworkMaxFailures: 5, windowSeconds: 900, blockSeconds: 900 }, enabled: false, rules: [], excludedIPs: [] }, revision: 1,
};
function configured() {
  const h = scriptedAdmin("/admin/settings");
  h.backend.settings = { get: vi.fn().mockResolvedValue(settings), save: vi.fn().mockImplementation(async (value: AdminSettings) => ({ ...value, revision: 2 })), reload: vi.fn() };
  h.backend.users.list = vi.fn().mockResolvedValue({ items: [] });
  return h;
}

describe("workspace usability", () => {
  it("finds pages by task words without exposing unavailable SQL tools", async () => {
    const h = configured(); h.history.replace("/admin/collections"); h.script.catalog.push({ value: { version: 0, collections: [] } });
    const user = userEvent.setup(); renderAdmin(h);
    await screen.findByText("No collections yet.");
    await user.type(screen.getByRole("searchbox", { name: "Find a page" }), "backup");
    const nav = within(screen.getByRole("navigation", { name: "Administration" }));
    expect(nav.getByRole("link", { name: "Backup storage" })).toBeVisible();
    expect(nav.getByRole("link", { name: "Settings" })).toBeVisible();
    expect(nav.queryByRole("link", { name: "Collections" })).toBeNull();
    expect(nav.queryByRole("link", { name: "SQL console" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(nav.getByRole("link", { name: "Collections" })).toBeVisible();
  });

  it("preserves a collection draft when a context-tab departure is declined", async () => {
    const h = scriptedAdmin("/admin/collections/posts/records/new");
    h.script.catalog.push({ value: { version: 1, collections: [collection("posts", "posts-id", [textField("title", 0)])] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup(); renderAdmin(h);
    await user.selectOptions(await screen.findByLabelText("title state"), "value");
    await user.type(screen.getByLabelText("title", { exact: true }), "Keep this draft");
    const schema = within(screen.getByRole("navigation", { name: "Collection workspace" })).getByRole("link", { name: "Schema" });
    expect(schema).toHaveAttribute("href", "/admin/collections/posts/edit");
    await user.click(schema);
    expect(h.history.path()).toBe("/admin/collections/posts/records/new");
    expect(screen.getByLabelText("title")).toHaveValue("Keep this draft");
    expect(confirm).toHaveBeenCalledOnce();
    expect(screen.getByRole("link", { name: "Back to records" })).toHaveAttribute("href", "/admin/collections/posts/records");
  });

  it("protects unsaved application settings and focuses the new page after an accepted departure", async () => {
    const h = configured(); const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup(); renderAdmin(h);
    await user.type(await screen.findByLabelText("Branding"), " changed");
    await user.click(screen.getByRole("link", { name: "Users" }));
    expect(h.history.path()).toBe("/admin/settings");
    expect(screen.getByLabelText("Branding")).toHaveValue("pgBase changed");
    confirm.mockReturnValue(true);
    await user.click(screen.getByRole("link", { name: "Users" }));
    await screen.findByText("No public users yet.");
    expect(h.history.path()).toBe("/admin/users");
    expect(document.getElementById("workspace-content")).toHaveFocus();
  });

  it("releases the application guard only after a successful save", async () => {
    const h = configured(); const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup(); renderAdmin(h);
    await user.type(await screen.findByLabelText("Branding"), " saved");
    await user.click(screen.getByRole("button", { name: "Save settings" }));
    await screen.findByText("Settings saved.");
    await user.click(screen.getByRole("link", { name: "Users" }));
    expect(h.history.path()).toBe("/admin/users"); expect(confirm).not.toHaveBeenCalled();
  });

  it("keeps unsaved IP policy protected after saving email independently", async () => {
    const h = configured(); const profile = { id: "admin", email: "admin@example.test", active: true, createdAt: "now", updatedAt: "now" };
    h.backend.profile = { get: vi.fn().mockResolvedValue(profile), ipAllowlist: vi.fn().mockResolvedValue([]), update: vi.fn().mockImplementation(async input => ({ ...profile, ...input })), setIPAllowlist: vi.fn().mockImplementation(async value => value) };
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false); const user = userEvent.setup(); renderAdmin(h);
    const email = await screen.findByLabelText("Email", { exact: true });
    await user.clear(email); await user.type(email, "changed@example.test");
    await user.type(screen.getByLabelText("Allowed CIDRs"), "10.0.0.0/8");
    await user.click(screen.getByRole("button", { name: "Save administrator email" }));
    await screen.findByText("Administrator email updated.");
    await user.click(screen.getByRole("link", { name: "Users" }));
    expect(h.history.path()).toBe("/admin/settings"); expect(confirm).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "Save IP allowlist" }));
    await screen.findByText("IP allowlist updated.");
    await user.click(screen.getByRole("link", { name: "Users" }));
    expect(h.history.path()).toBe("/admin/users"); expect(confirm).toHaveBeenCalledOnce();
  });

  it("protects a file-only record draft without intercepting its successful save", async () => {
    const h = scriptedAdmin("/admin/collections/notes/records/new");
    const notes = collection("notes", "notes-id", [{ name: "attachment", type: "file", ordinal: 0, required: false, unique: false, indexed: false }]);
    h.script.catalog.push({ value: { version: 1, collections: [notes] } }, { value: { version: 1, collections: [notes] } });
    h.backend.records.createWithFiles = vi.fn().mockResolvedValue({ data: { id: "saved" } });
    h.script.list.push({ value: { items: [] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false); const user = userEvent.setup(); renderAdmin(h);
    await user.upload(await screen.findByLabelText("attachment files for save"), new File(["note"], "note.txt", { type: "text/plain" }));
    await user.click(screen.getByRole("link", { name: "Back to records" }));
    expect(h.history.path()).toBe("/admin/collections/notes/records/new"); expect(confirm).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "Create record" }));
    await waitFor(() => expect(h.history.path()).toBe("/admin/collections/notes/records"));
    expect(h.backend.records.createWithFiles).toHaveBeenCalledOnce(); expect(confirm).toHaveBeenCalledOnce();
  });

  it("closes the mobile navigation with Escape and restores the toggle focus", async () => {
    const h = scriptedAdmin(); h.script.catalog.push({ value: { version: 0, collections: [] } });
    const user = userEvent.setup(); renderAdmin(h);
    await screen.findByText("No collections yet.");
    const toggle = screen.getByRole("button", { name: "Toggle navigation" });
    await user.click(toggle);
    expect(screen.getByRole("dialog", { name: "Workspace navigation" })).toBeVisible();
    expect(screen.getByRole("searchbox", { name: "Find a page" })).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull(); expect(toggle).toHaveFocus();
  });

  it("keeps the drawer open when discarding settings is declined, and closes it after an accepted move", async () => {
    const h = configured(); const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup(); renderAdmin(h);
    await user.type(await screen.findByLabelText("Branding"), " draft");
    await user.click(screen.getByRole("button", { name: "Toggle navigation" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("link", { name: "Users" }));
    expect(h.history.path()).toBe("/admin/settings");
    expect(screen.getByRole("dialog")).toBeVisible();
    confirm.mockReturnValue(true);
    const contentFocusWhileInert: boolean[] = [];
    const originalFocus = HTMLElement.prototype.focus;
    vi.spyOn(HTMLElement.prototype, "focus").mockImplementation(function (this: HTMLElement, options?: FocusOptions) {
      if (this.id === "workspace-content") contentFocusWhileInert.push(this.closest("[inert]") !== null);
      originalFocus.call(this, options);
    });
    await user.click(within(screen.getByRole("dialog")).getByRole("link", { name: "Users" }));
    await screen.findByText("No public users yet.");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.getElementById("workspace-content")).toHaveFocus();
    expect(contentFocusWhileInert).toEqual([false]);
  });

  it("protects an invalid, unsaved backup retention input", async () => {
    const h = scriptedAdmin("/admin/storage/backups");
    h.backend.backupStorage.get = vi.fn().mockResolvedValue({ driver: "local", bucket: "", prefix: "", region: "", endpoint: "", accessKey: "", usePathStyle: false, retention: 7, secretConfigured: false, revision: 1 });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false); const user = userEvent.setup(); renderAdmin(h);
    const retention = await screen.findByLabelText("Backups to retain");
    await user.clear(retention);
    await user.click(screen.getByRole("link", { name: "Users" }));
    expect(h.history.path()).toBe("/admin/storage/backups");
    expect(retention).toHaveValue(null); expect(confirm).toHaveBeenCalledOnce();
  });
});
