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
});
