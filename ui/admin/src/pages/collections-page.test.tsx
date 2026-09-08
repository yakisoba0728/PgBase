import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { FieldDefinition } from "../backend/ports";
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
    expect(screen.getAllByRole("listitem").map((item) => item.textContent?.slice(0, 5))).toEqual(["posts", "users"]);

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

  it("blocks user access for a scalar collection targeted by an incoming relation", async () => {
    const harness = scriptedAdmin("/admin/collections");
    const targets = collection("targets", "targets-id", [textField("name", 0)]);
    const sources = collection("sources", "sources-id", [{ ...owner, relation: { ...owner.relation!, targetCollectionId: "targets-id" } }]);
    harness.script.catalog.push({ value: { version: 4, collections: [targets, sources] } });
    renderAdmin(harness);
    expect((await screen.findAllByText("Public user records are blocked while this collection has incoming or outgoing relations.")).length).toBeGreaterThan(1);
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
