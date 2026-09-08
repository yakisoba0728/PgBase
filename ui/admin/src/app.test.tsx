import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { callsTo, deferredStep, scriptedHealth, scriptedStatus } from "./test/backend-script";
import { collection, renderAdmin, scriptedAdmin, textField } from "./test/harness";

const posts = collection("posts", "posts-id", [textField("title", 0)]);
const users = collection("users", "users-id", [textField("name", 0)]);
const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";

afterEach(() => vi.restoreAllMocks());

describe("administration shell", () => {
  it("swaps the record list between collections as history moves", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/records");
    for (const _ of [0, 1, 2]) harness.script.catalog.push({ value: { version: 4, collections: [posts, users] } });
    harness.script.list.push({ value: { items: [{ id: "post-id", title: "Post A" }] } });
    harness.script.list.push({ value: { items: [{ id: "user-id", name: "User B" }] } });
    harness.script.list.push({ value: { items: [{ id: "post-id", title: "Post A" }] } });
    renderAdmin(harness);

    expect(await screen.findByText("Post A")).toBeVisible();

    await act(async () => { harness.history.push("/admin/collections/users/records"); });
    expect(await screen.findByRole("heading", { name: "users records" })).toBeVisible();
    expect(await screen.findByText("User B")).toBeVisible();
    expect(screen.queryByText("Post A")).not.toBeInTheDocument();

    await act(async () => { harness.history.back(); });
    expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
    expect(await screen.findByText("Post A")).toBeVisible();
    expect(screen.getByLabelText("Filter")).toHaveValue("");
  });

  it("moves between the collection list and the read-only system page", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({ value: { version: 9, collections: [] } });
    harness.script.status.push({ value: scriptedStatus() });
    harness.script.health.push({ value: scriptedHealth() });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Collections" });
    await user.click(screen.getByRole("link", { name: "System" }));

    expect(await screen.findByRole("heading", { name: "System" })).toBeVisible();
    expect(harness.history.path()).toBe("/admin/system");
  });

  it("reaches the schema operation history as a typed route a new tab could open", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({ value: { version: 9, collections: [] } });
    harness.script.operations.push({ value: { items: [] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Collections" });
    const link = screen.getByRole("link", { name: "Schema operations" });
    expect(link).toHaveAttribute("href", "/admin/schema-operations");
    await user.click(link);

    expect(await screen.findByRole("heading", { name: "Schema operations" })).toBeVisible();
    expect(harness.history.path()).toBe("/admin/schema-operations");
  });

  it("keeps a record draft the administrator refused to lose and lets it go when asked again", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/records");
    for (const _ of [0, 1, 2]) harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.list.push({ value: { items: [{ id: recordID, title: "Post A" }] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "Post A" }, etag: '"pgb1.one"' } });
    harness.script.list.push({ value: { items: [{ id: recordID, title: "Post A" }] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.click(await screen.findByRole("link", { name: recordID }));
    await user.clear(await screen.findByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "unsaved edit");

    await act(async () => { harness.history.back(); });
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("title")).toHaveValue("unsaved edit");
    expect(harness.history.path()).toBe(`/admin/collections/posts/records/${recordID}`);

    await user.click(screen.getByRole("link", { name: "Collections" }));
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(screen.getByLabelText("title")).toHaveValue("unsaved edit");
    expect(harness.history.path()).toBe(`/admin/collections/posts/records/${recordID}`);

    confirm.mockReturnValue(true);
    await act(async () => { harness.history.back(); });
    expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
  });

  it("keeps a collection draft the administrator refused to lose and applies it without asking", async () => {
    const harness = scriptedAdmin("/admin/collections");
    for (const _ of [0, 1, 2]) harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.plan.push({ value: { baseVersion: 4, checksum: "approved", sql: ["ALTER TABLE posts"], fields: [
      { name: "title", type: "text", required: false, unique: false, indexed: true },
      { name: "headline", type: "text", required: false, unique: false, indexed: false },
    ] } });
    harness.script.apply.push({ value: undefined });
    harness.script.list.push({ value: { items: [] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.click(await screen.findByRole("link", { name: "Edit" }));
    await user.click(await screen.findByRole("button", { name: "Add field" }));
    await user.type(screen.getByLabelText("Field 2 name"), "headline");

    await act(async () => { harness.history.back(); });
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("Field 2 name")).toHaveValue("headline");
    expect(harness.history.path()).toBe("/admin/collections/posts/edit");

    await user.click(screen.getByRole("button", { name: "Preview change" }));
    await user.click(await screen.findByRole("button", { name: "Apply change" }));

    expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
    expect(confirm).toHaveBeenCalledTimes(1);
  });

  it("never asks about a draft the administrator has already saved", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    for (const _ of [0, 1]) harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "Post A" }, etag: '"pgb1.one"' } });
    harness.script.update.push({ value: { data: { id: recordID, title: "saved" }, etag: '"pgb1.two"' } });
    harness.script.list.push({ value: { items: [{ id: recordID, title: "saved" }] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.clear(await screen.findByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "saved");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
    expect(confirm).not.toHaveBeenCalled();
  });

  it("never asks about a draft the administrator deleted the record for", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    for (const _ of [0, 1]) harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "Post A" }, etag: '"pgb1.one"' } });
    harness.script.remove.push({ value: undefined });
    harness.script.list.push({ value: { items: [] } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.clear(await screen.findByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "unsaved edit");
    await user.click(screen.getByRole("button", { name: "Delete record" }));

    expect(await screen.findByRole("heading", { name: "posts records" })).toBeVisible();
    // Only the deletion itself was confirmed: the draft went with the record.
    expect(confirm).toHaveBeenCalledTimes(1);
  });

  it("never asks about a draft the administrator dropped the collection for", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/edit");
    for (const _ of [0, 1]) harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.plan.push({ value: { baseVersion: 4, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    harness.script.apply.push({ value: undefined });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.click(await screen.findByRole("button", { name: "Add field" }));
    await user.type(screen.getByLabelText("Field 2 name"), "headline");
    await user.click(screen.getByRole("button", { name: "Delete collection" }));
    await user.type(await screen.findByLabelText("Type posts to confirm"), "posts");
    await user.click(screen.getByRole("button", { name: "Confirm delete" }));

    expect(await screen.findByRole("heading", { name: "Collections" })).toBeVisible();
    expect(confirm).not.toHaveBeenCalled();
  });

  it("does not sign out while a draft the administrator kept is still unsaved", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "Post A" }, etag: '"pgb1.one"' } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.clear(await screen.findByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "unsaved edit");
    await user.click(screen.getByRole("button", { name: "Sign out" }));

    expect(confirm).toHaveBeenCalledTimes(1);
    expect(callsTo(harness.script, "logout")).toHaveLength(0);
    expect(screen.getByLabelText("title")).toHaveValue("unsaved edit");
  });

  it("keeps protecting a confirmed draft when sign-out fails", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "Post A" }, etag: '"pgb1.one"' } });
    harness.script.logout.push({ fault: { kind: "unreachable" } }, { fault: { kind: "unreachable" } });
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.clear(await screen.findByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "unsaved edit");
    await user.click(screen.getByRole("button", { name: "Sign out" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to sign out. Try again.");
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("title")).toHaveValue("unsaved edit");

    confirm.mockReturnValue(false);
    await user.click(screen.getByRole("button", { name: "Sign out" }));

    expect(confirm).toHaveBeenCalledTimes(2);
    expect(callsTo(harness.script, "logout")).toHaveLength(1);
    expect(screen.getByLabelText("title")).toHaveValue("unsaved edit");
  });

  it("makes the workspace inert while logout is pending and always settles after backend success", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "Post A" }, etag: '"pgb1.one"' } });
    const logout = deferredStep<void>();
    harness.script.logout.push(logout.step);
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    const user = userEvent.setup();
    renderAdmin(harness);

    const title = await screen.findByLabelText("title");
    await user.clear(title);
    await user.type(title, "protected draft");
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    expect(screen.getByRole("button", { name: "Signing out…" })).toBeDisabled();
    expect(screen.getByRole("group", { name: "Administrator workspace" })).toHaveAttribute("inert");
    expect(title).toBeDisabled();

    await user.type(title, " attempted edit");
    await user.click(screen.getByRole("link", { name: "System" }));
    expect(title).toHaveValue("protected draft");
    expect(harness.history.path()).toBe(`/admin/collections/posts/records/${recordID}`);

    await act(async () => { logout.settle({ value: undefined }); });

    expect(await screen.findByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(harness.history.path()).toBe("/admin/login");
  });

  it("refuses a second sign-out and keeps the session after a failed one", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({ value: { version: 0, collections: [] } });
    const logout = deferredStep<void>();
    harness.script.logout.push(logout.step);
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "pgBase administration" });
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    const pending = screen.getByRole("button", { name: "Signing out…" });
    expect(pending).toBeDisabled();
    await user.click(pending);

    await act(async () => { logout.settle({ fault: { kind: "unreachable" } }); });

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to sign out. Try again.");
    expect(screen.getByRole("button", { name: "Sign out" })).toBeEnabled();
    expect(screen.getByRole("heading", { name: "pgBase administration" })).toBeVisible();
  });

  it("keeps guarding an unsaved draft after sign-out fails", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    for (const _ of [0, 1]) harness.script.catalog.push({ value: { version: 4, collections: [posts] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "Post A" }, etag: '"pgb1.one"' } });
    const logout = deferredStep<void>();
    harness.script.logout.push(logout.step);
    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(true).mockReturnValueOnce(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.clear(await screen.findByLabelText("title"));
    await user.type(screen.getByLabelText("title"), "unsaved edit");
    await user.click(screen.getByRole("button", { name: "Sign out" }));
    await act(async () => { logout.settle({ fault: { kind: "unreachable" } }); });
    await screen.findByRole("alert");

    await user.type(screen.getByLabelText("title"), " after failure");
    const unload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(unload);
    expect(unload.defaultPrevented).toBe(true);

    await user.click(screen.getByRole("link", { name: "Collections" }));
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(screen.getByLabelText("title")).toHaveValue("unsaved edit after failure");
    expect(harness.history.path()).toBe(`/admin/collections/posts/records/${recordID}`);
  });

  it("falls back to the collection list for an undecodable administrator path", async () => {
    const harness = scriptedAdmin("/admin/collections/%E0%A4%A/records");
    harness.script.catalog.push({ value: { version: 9, collections: [] } });
    renderAdmin(harness);

    expect(await screen.findByRole("heading", { name: "Collections" })).toBeVisible();
  });

  it("signs the administrator out when a protected request reports the session expired", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({ fault: { kind: "unauthorized" } });
    renderAdmin(harness);

    expect(await screen.findByRole("heading", { name: "Sign in to pgBase" })).toBeVisible();
    await waitFor(() => expect(harness.history.path()).toBe("/admin/login"));
  });
});
