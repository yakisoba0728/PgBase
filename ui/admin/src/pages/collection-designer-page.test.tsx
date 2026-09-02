import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { callsTo, deferredStep, type ScriptedApply } from "../test/backend-script";
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

  it("shows the pinned deletion target and keeps confirming disabled until it is typed", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    harness.script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    harness.script.apply.push({ value: undefined });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("posts · physical table posts")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Delete collection" }));

    expect(await screen.findByText("Deleting posts · physical table posts · catalog version 7")).toBeVisible();
    expect(screen.getByRole("button", { name: "Confirm delete" })).toBeDisabled();

    await user.type(screen.getByLabelText("Type posts to confirm"), "post");
    expect(screen.getByRole("button", { name: "Confirm delete" })).toBeDisabled();

    await user.type(screen.getByLabelText("Type posts to confirm"), "s");
    await user.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() => expect(callsTo(harness.script, "apply")).toHaveLength(1));
    expect(callsTo(harness.script, "apply")[0].detail.request).toEqual({ operation: "delete", name: "posts", checksum: "drop", baseVersion: 7 });
  });

  it("reports a collection the catalog does not have", async () => {
    const harness = scriptedAdmin("/admin/collections/ghosts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    renderAdmin(harness);

    expect(await screen.findByRole("alert")).toHaveTextContent("Collection not found.");
  });

  it("tells the administrator an apply is not decided yet and offers the ledger instead of a verdict", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    harness.script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [{ name: "title", type: "text", required: true, unique: false, indexed: true }], sql: ["ALTER TABLE posts"] } });
    harness.script.apply.push({ value: { status: "unknown" } });
    harness.script.operation.push({
      value: {
        operationId: "op-1", operation: "collection_update", collectionId: "posts-id", status: "running",
        baseVersion: 7, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:01Z",
      },
    });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("posts · physical table posts")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Preview change" }));
    await user.click(await screen.findByRole("button", { name: "Apply change" }));

    const recovery = await screen.findByRole("status", { name: "Schema operation recovery" });
    expect(recovery).toHaveTextContent("its outcome is not known yet");
    // Neither verdict may be shown while the ledger has not given one.
    expect(recovery).not.toHaveTextContent("The change was applied");
    expect(recovery).not.toHaveTextContent("did not apply");
    expect(within(recovery).getByRole("link", { name: "View schema operations" }))
      .toHaveAttribute("href", "/admin/schema-operations");
    expect(screen.getByRole("heading", { name: "Edit collection" })).toBeVisible();
  });

  it("reports the committed outcome once the ledger confirms an apply it could not answer", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    harness.script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [{ name: "title", type: "text", required: true, unique: false, indexed: true }], sql: ["ALTER TABLE posts"] } });
    harness.script.apply.push({ value: { status: "running" } });
    harness.script.operation.push({
      value: {
        operationId: "op-1", operation: "collection_update", collectionId: "posts-id", status: "committed",
        baseVersion: 7, committedVersion: 8, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:31Z",
      },
    });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("posts · physical table posts")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Preview change" }));
    await user.click(await screen.findByRole("button", { name: "Apply change" }));

    expect(await screen.findByText(/The change was applied\./)).toBeVisible();
  });

  it("says the draft on screen is unsaved beside the commit the ledger reports", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/edit");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    harness.script.plan.push({ value: { baseVersion: 7, checksum: "approved", fields: [{ name: "title", type: "text", required: true, unique: false, indexed: true }], sql: ["ALTER TABLE posts"] } });
    const application = deferredStep<ScriptedApply | undefined>();
    harness.script.apply.push(application.step);
    harness.script.operation.push({
      value: {
        operationId: "op-1", operation: "collection_update", collectionId: "posts-id", status: "committed",
        baseVersion: 7, committedVersion: 8, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:31Z",
      },
    });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("posts · physical table posts")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Preview change" }));
    await user.click(await screen.findByRole("button", { name: "Apply change" }));

    // The administrator keeps typing while the apply is still in flight.
    await user.clear(screen.getByLabelText("Field 1 name"));
    await user.type(screen.getByLabelText("Field 1 name"), "new_unsaved_title");
    application.settle({ value: { status: "unknown" } });

    const recovery = await screen.findByRole("status", { name: "Schema operation recovery" });
    // The panel names the identity the client minted, so match the verdict only.
    await waitFor(() => expect(recovery).toHaveTextContent(/committed\./));
    // Without the alert the page reports a commit next to a draft that never
    // went with it, which reads as if this draft were saved too.
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "The change was applied. The edits you made after sending it are not saved yet",
    );
    expect(screen.getByLabelText("Field 1 name")).toHaveValue("new_unsaved_title");
  });
});
