import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FieldDefinition } from "../backend/ports";
import { callsTo, deferredStep } from "../test/backend-script";
import { collection, renderAdmin, scriptedAdmin, textField } from "../test/harness";

const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const targetID = "11111111-1111-4111-8111-111111111111";
const otherTargetID = "22222222-2222-4222-8222-222222222222";

const tags: FieldDefinition = {
  name: "tags", type: "relation", required: false, unique: false, indexed: true, ordinal: 1,
  relation: { targetCollectionId: "tags-id", cardinality: "multiple", minSelect: 0, maxSelect: 3, onDelete: "restrict" },
};
const posts = collection("posts", "posts-id", [textField("title", 0), tags]);
const tagsCollection = collection("tags", "tags-id", [textField("label", 0, { unique: true })]);

afterEach(() => vi.restoreAllMocks());

describe("record editor page", () => {
  it("keeps edit actions hidden until the requested record has loaded", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts, tagsCollection] } });
    const read = deferredStep<{ data: Record<string, string>; etag: string }>();
    harness.script.read.push(read.step);
    renderAdmin(harness);

    expect(await screen.findByText("Loading record…")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Delete record" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("title")).not.toBeInTheDocument();

    await act(async () => { read.settle({ value: { data: { id: recordID, title: "loaded" }, etag: '"pgb1.one"' } }); });

    expect(await screen.findByLabelText("title")).toHaveValue("loaded");
    expect(screen.getByRole("button", { name: "Save changes" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Delete record" })).toBeEnabled();
  });

  it("does not delete until the administrator confirms", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts, tagsCollection] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "loaded" }, etag: '"pgb1.one"' } });
    vi.spyOn(window, "confirm").mockReturnValue(false);
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.click(await screen.findByRole("button", { name: "Delete record" }));
    expect(callsTo(harness.script, "remove")).toHaveLength(0);
  });

  it("selects a relation target from the picker into the ordered draft", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts, tagsCollection] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "loaded", tags: [] }, etag: '"pgb1.one"' } });
    harness.script.list.push({ value: { items: [{ id: targetID, label: "release" }] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.type(await screen.findByLabelText("tags target label"), "release");
    await user.click(screen.getByRole("button", { name: "Search tags targets" }));
    await user.click(await screen.findByRole("option", { name: `release — ${targetID}` }));

    await waitFor(() => expect(screen.getByLabelText("tags")).toHaveValue(targetID));
    expect(callsTo(harness.script, "list")[0].detail).toEqual({ collection: "tags", filter: "label = 'release'", sort: "label" });
  });

  it("picks a relation target with the keyboard alone", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts, tagsCollection] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "loaded", tags: [] }, etag: '"pgb1.one"' } });
    harness.script.list.push({ value: { items: [{ id: targetID, label: "release" }, { id: otherTargetID, label: "beta" }] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    const search = await screen.findByLabelText("tags target label");
    await user.click(search);
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("listbox", { name: "tags target results" })).toBeVisible();

    await user.keyboard("{ArrowDown}{ArrowDown}");
    expect(search).toHaveAttribute("aria-activedescendant", "record-tags-target-option-1");
    await user.keyboard("{Enter}");

    await waitFor(() => expect(screen.getByLabelText("tags")).toHaveValue(otherTargetID));
    expect(screen.getByRole("button", { name: `Remove beta — ${otherTargetID}` })).toBeVisible();
  });

  it("keeps a selected target that the target collection no longer returns", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts, tagsCollection] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "loaded", tags: [targetID] }, etag: '"pgb1.one"' } });
    harness.script.list.push({ value: { items: [] } });
    renderAdmin(harness);

    expect(await screen.findByText(`${targetID} — unavailable`)).toBeVisible();
    expect(screen.getByLabelText("tags")).toHaveValue(targetID);
  });
});
