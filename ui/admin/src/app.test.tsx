import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { deferredStep } from "./test/backend-script";
import { collection, renderAdmin, scriptedAdmin, textField } from "./test/harness";

const posts = collection("posts", "posts-id", [textField("title", 0)]);
const users = collection("users", "users-id", [textField("name", 0)]);

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
    harness.script.status.push({ value: { version: "v1.2.3", databaseReachable: true, migrationCurrent: 4, migrationLatest: 5, migrationDirty: true, catalogVersion: 9, listenerReady: false } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Collections" });
    await user.click(screen.getByRole("button", { name: "System" }));

    expect(await screen.findByRole("heading", { name: "System" })).toBeVisible();
    expect(harness.history.path()).toBe("/admin/system");
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
