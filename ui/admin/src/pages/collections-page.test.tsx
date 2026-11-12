import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { FieldDefinition } from "../backend/ports";
import { callsTo } from "../test/backend-script";
import { collection, renderAdmin, scriptedAdmin, textField } from "../test/harness";

const owner: FieldDefinition = {
  name: "owner", type: "relation", required: false, unique: false, indexed: true, ordinal: 1,
  relation: { targetCollectionId: "users-id", cardinality: "single", minSelect: 0, maxSelect: 1, onDelete: "restrict" },
};

describe("collections page", () => {
  it("lists collections in name order with their field and relation counts", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({
      value: {
        version: 5,
        collections: [
          collection("users", "users-id", [textField("name", 0)]),
          collection("posts", "posts-id", [textField("title", 0), owner]),
        ],
      },
    });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("Catalog version 5")).toBeVisible();
    expect(screen.getByText("2 fields · 1 relation")).toBeVisible();
    expect(screen.getByText("1 fields")).toBeVisible();
    expect(within(screen.getByRole("list", { name: "Collections" })).getAllByRole("listitem").map((item) => item.textContent?.slice(0, 5))).toEqual(["posts", "users"]);

    await user.click(screen.getByRole("link", { name: "posts" }));
    expect(harness.history.path()).toBe("/admin/collections/posts/records");
  });

  it("ends the loading state when the catalog cannot be read", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({ fault: { kind: "unreachable" } });
    renderAdmin(harness);

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load collections.");
    expect(screen.queryByText("Loading collections…")).not.toBeInTheDocument();
  });

  it("says so when the instance has no collections yet", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({ value: { version: 0, collections: [] } });
    renderAdmin(harness);

    expect(await screen.findByText("No collections yet.")).toBeVisible();
  });

  it("loads and updates user access for collections with incoming and outgoing relations", async () => {
    const harness = scriptedAdmin("/admin/collections");
    const targets = collection("targets", "targets-id", [textField("name", 0)]);
    const sources = collection("sources", "sources-id", [{ ...owner, relation: { ...owner.relation!, targetCollectionId: "targets-id" } }]);
    harness.script.catalog.push({ value: { version: 4, collections: [targets, sources] } });
    harness.script.userAccess.push(
      { value: { collection: "sources", enabled: false } },
      { value: { collection: "targets", enabled: true } },
    );
    harness.script.setUserAccess.push(
      { value: { collection: "sources", enabled: true } },
      { value: { collection: "targets", enabled: false } },
    );
    const user = userEvent.setup();
    renderAdmin(harness);

    const source = within((await screen.findByRole("link", { name: "sources" })).closest("li")!);
    const target = within(screen.getByRole("link", { name: "targets" }).closest("li")!);
    expect(await source.findByRole("button", { name: "Enable user access" })).toBeEnabled();
    expect(await target.findByRole("button", { name: "Disable user access" })).toBeEnabled();
    expect(callsTo(harness.script, "userAccess").map((call) => call.detail)).toEqual([
      { collection: "sources" }, { collection: "targets" },
    ]);

    await user.click(source.getByRole("button", { name: "Enable user access" }));
    expect(await source.findByRole("button", { name: "Disable user access" })).toBeEnabled();
    await user.click(target.getByRole("button", { name: "Disable user access" }));
    expect(await target.findByRole("button", { name: "Enable user access" })).toBeEnabled();
    expect(callsTo(harness.script, "setUserAccess").map((call) => call.detail)).toEqual([
      { collection: "sources", enabled: true }, { collection: "targets", enabled: false },
    ]);
  });

  it("retries an initial user access policy read", async () => {
    const harness = scriptedAdmin("/admin/collections");
    harness.script.catalog.push({ value: { version: 4, collections: [collection("notes", "notes-id", [textField("title", 0)])] } });
    harness.script.userAccess.push({ fault: { kind: "unreachable" } }, { value: { collection: "notes", enabled: false } });
    const user = userEvent.setup();
    renderAdmin(harness);
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load user access.");
    await user.click(screen.getByRole("button", { name: "Retry user access" }));
    expect(await screen.findByRole("button", { name: "Enable user access" })).toBeEnabled();
  });
});
