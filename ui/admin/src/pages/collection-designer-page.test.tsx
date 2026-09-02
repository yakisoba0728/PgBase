import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { callsTo } from "../test/backend-script";
import { collection, renderAdmin, scriptedAdmin, textField } from "../test/harness";

const users = collection("users", "users-id");
const posts = collection("posts", "posts-id", [textField("title", 0, { required: true })]);

describe("collection designer page", () => {
  it("previews a self relation built from the live catalog", async () => {
    const harness = scriptedAdmin("/admin/collections/new");
    harness.script.catalog.push({ value: { version: 4, collections: [users] } });
    harness.script.plan.push({
      value: {
        baseVersion: 4,
        checksum: "relation-checksum",
        sql: ["ALTER TABLE app.nodes ADD CONSTRAINT relation_parent ..."],
        fields: [{
          name: "parent", type: "relation", required: false, unique: false, indexed: true,
          relation: { targetCollection: "nodes", cardinality: "single", minSelect: 0, maxSelect: 1, onDelete: "set_null" },
        }],
      },
    });
    const user = userEvent.setup();
    renderAdmin(harness);

    await screen.findByRole("heading", { name: "Create collection" });
    await user.type(screen.getByLabelText("Collection name"), "nodes");
    await user.type(screen.getByLabelText("Field 1 name"), "parent");
    await user.selectOptions(screen.getByLabelText("Field 1 type"), "relation");

    const target = await screen.findByLabelText("Field 1 relation target");
    await waitFor(() => expect(target).toBeEnabled());
    expect(screen.getByRole("option", { name: "nodes (self)" })).toBeVisible();
    expect(screen.getByRole("option", { name: "users" })).toBeVisible();
    await user.selectOptions(target, "nodes");
    await user.selectOptions(screen.getByLabelText("Field 1 relation delete action"), "set_null");
    expect(screen.getByLabelText("Field 1 required")).toBeDisabled();
    expect(screen.getByLabelText("Field 1 indexed")).toBeChecked();
    expect(screen.getByLabelText("Field 1 indexed")).toBeDisabled();
    expect(screen.getByLabelText("Field 1 has default")).toBeDisabled();

    await user.click(screen.getByRole("button", { name: "Preview collection" }));
    expect(await screen.findByText("parent · relation · indexed · target: nodes · single · min: 0 · max: 1 · on delete: set_null")).toBeVisible();
    expect(screen.getByText(/ALTER TABLE app\.nodes/)).toBeVisible();
    expect(callsTo(harness.script, "plan")[0].detail.change).toEqual({
      operation: "create",
      name: "nodes",
      fields: [{
        name: "parent", type: "relation", required: false, unique: false, indexed: true,
        relation: { targetCollection: "nodes", cardinality: "single", minSelect: 0, maxSelect: 1, onDelete: "set_null" },
      }],
    });
  });

  it("gates a destructive change behind the confirmation the designer reports", async () => {
    const twoFields = collection("posts", "posts-id", [textField("title", 0, { required: true }), textField("subtitle", 1)]);
    const harness = scriptedAdmin("/admin/collections/posts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [twoFields] } });
    harness.script.plan.push({
      value: {
        baseVersion: 7,
        checksum: "approved",
        sql: ["ALTER TABLE posts DROP COLUMN subtitle"],
        fields: [{ name: "title", type: "text", required: true, unique: false, indexed: true }],
      },
    });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("posts · physical table posts")).toBeVisible();
    await user.click(screen.getAllByRole("button", { name: "Remove field" })[1]);
    await user.click(screen.getByRole("button", { name: "Preview change" }));

    const warning = await screen.findByRole("alert", { name: "Permanent data loss warning" });
    expect(warning).toHaveTextContent("subtitle");
    expect(screen.getByRole("button", { name: "Apply change" })).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: /permanently deleted/ }));
    expect(screen.getByRole("button", { name: "Apply change" })).toBeEnabled();
  });

  it("shows the deletion plan before dropping a collection and lets it be cancelled", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    harness.script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("posts · physical table posts")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Delete collection" }));

    expect(await screen.findByText("DROP TABLE posts")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("DROP TABLE posts")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete collection" })).toBeVisible();
  });

  it("reports a collection the catalog does not have", async () => {
    const harness = scriptedAdmin("/admin/collections/ghosts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    renderAdmin(harness);

    expect(await screen.findByRole("alert")).toHaveTextContent("Collection not found.");
  });
});
