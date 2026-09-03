import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { LosslessNumber } from "lossless-json";
import type { FieldDefinition } from "../backend/ports";
import { callsTo } from "../test/backend-script";
import { collection, renderAdmin, scriptedAdmin, textField } from "../test/harness";

const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const ownerID = "abcdefab-cdef-4abc-8def-abcdefabcdef";
const firstTag = "11111111-1111-4111-8111-111111111111";
const secondTag = "22222222-2222-4222-8222-222222222222";

function relationField(name: string, ordinal: number, cardinality: "single" | "multiple", targetCollectionId: string): FieldDefinition {
  return {
    name, type: "relation", required: false, unique: false, indexed: true, ordinal,
    relation: { targetCollectionId, cardinality, minSelect: 0, maxSelect: cardinality === "single" ? 1 : 3, onDelete: "restrict" },
  };
}

const posts = collection("posts", "posts-id", [
  textField("title", 0),
  relationField("owner", 1, "single", "users-id"),
  relationField("tags", 2, "multiple", "tags-id"),
]);

describe("records page", () => {
  it("renders a value outside the JavaScript safe integer range without rounding it", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/records");
    harness.script.catalog.push({ value: { version: 4, collections: [collection("posts", "posts-id", [{ name: "views", type: "bigint", required: false, unique: false, indexed: false, ordinal: 0 }])] } });
    harness.script.list.push({ value: { items: [{ id: recordID, views: new LosslessNumber("9007199254740993") }] } });
    renderAdmin(harness);

    expect(await screen.findByText("9007199254740993")).toBeVisible();
    expect(screen.queryByText("9007199254740992")).not.toBeInTheDocument();
  });

  it("renders ordered relations and offers sorting only where the server can order", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/records");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    harness.script.list.push({ value: { items: [{ id: recordID, title: "post", owner: ownerID, tags: [secondTag, firstTag] }] } });
    harness.script.list.push({ value: { items: [] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    const list = await screen.findByRole("list", { name: "tags related record IDs" });
    expect(within(list).getAllByRole("listitem").map((item) => item.textContent)).toEqual([secondTag, firstTag]);
    expect(screen.getByRole("button", { name: "owner" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "tags" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "owner" }));
    expect(await screen.findByText("No records yet.")).toBeVisible();
    expect(callsTo(harness.script, "list").at(-1)?.detail).toEqual({ collection: "posts", sort: "owner" });
  });

  it("offers Previous only once the cursor chain has moved off the first page", async () => {
    const notes = collection("notes", "notes-id", [textField("title", 0)]);
    const harness = scriptedAdmin("/admin/collections/notes/records");
    harness.script.catalog.push({ value: { version: 7, collections: [notes] } });
    harness.script.list.push({ value: { items: [{ id: recordID, title: "first" }], nextCursor: "cursor one" } });
    harness.script.list.push({ value: { items: [{ id: ownerID, title: "second" }] } });
    harness.script.list.push({ value: { items: [{ id: recordID, title: "first" }], nextCursor: "cursor one" } });
    const user = userEvent.setup();
    renderAdmin(harness);

    expect(await screen.findByText("first")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Previous page" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(await screen.findByText("second")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Next page" })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Previous page" }));
    expect(await screen.findByText("first")).toBeVisible();
    expect(callsTo(harness.script, "list").map((call) => call.detail)).toEqual([
      { collection: "notes" },
      { collection: "notes", cursor: "cursor one" },
      { collection: "notes" },
    ]);
  });

  it("reports a list it cannot load instead of loading forever", async () => {
    const harness = scriptedAdmin("/admin/collections/posts/records");
    harness.script.catalog.push({ value: { version: 7, collections: [posts] } });
    harness.script.list.push({ fault: { kind: "unreachable" } });
    renderAdmin(harness);

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load records.");
    expect(screen.queryByText("Loading records…")).not.toBeInTheDocument();
  });
});
