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
const requiredAttachment: FieldDefinition = { name: "attachment", type: "file", required: true, unique: false, indexed: false, ordinal: 1, options: { maxSelect: 2 } };
const documents = collection("documents", "documents-id", [textField("title", 0), requiredAttachment]);

afterEach(() => vi.restoreAllMocks());

describe("record editor page", () => {
	it("requires and atomically creates a record with its selected files", async () => {
		const harness = scriptedAdmin("/admin/collections/documents/records/new");
		harness.script.catalog.push({ value: { version: 4, collections: [documents] } });
		harness.script.createWithFiles.push({ value: { data: { id: recordID, title: "contract" }, etag: '"pgb1.atomic"' } });
		const user = userEvent.setup(); renderAdmin(harness);
		await user.selectOptions(await screen.findByLabelText("title state"), "value");
		await user.type(screen.getByLabelText("title"), "contract");
		await user.click(screen.getByRole("button", { name: "Create record" }));
		expect(await screen.findByText("Select at least 1 file.")).toBeVisible();
		expect(callsTo(harness.script, "createWithFiles")).toHaveLength(0);

		const input = screen.getByLabelText("attachment files for save");
		await user.upload(input, [new File(["a"], "a.txt"), new File(["b"], "b.txt"), new File(["c"], "c.txt")]);
		await user.click(screen.getByRole("button", { name: "Create record" }));
		expect(await screen.findByText("Select no more than 2 files.")).toBeVisible();
		expect(callsTo(harness.script, "createWithFiles")).toHaveLength(0);
		await user.upload(input, new File(["signed"], "contract.txt", { type: "text/plain" }));
		await user.click(screen.getByRole("button", { name: "Create record" }));
		await waitFor(() => expect(callsTo(harness.script, "createWithFiles")).toHaveLength(1));
		expect(callsTo(harness.script, "createWithFiles")[0].detail).toMatchObject({ collection: "documents", input: { title: "contract" }, files: { attachment: ["contract.txt"] } });
	});

	it("shows a local preview for selected media before the atomic file save", async () => {
		const imageField: FieldDefinition = { ...requiredAttachment, options: { maxSelect: 1, mimeTypes: ["image/png"] } };
		const images = collection("images", "images-id", [textField("title", 0), imageField]); const harness = scriptedAdmin("/admin/collections/images/records/new"); harness.script.catalog.push({ value: { version: 4, collections: [images] } });
		vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:selected-image"); const user = userEvent.setup(); renderAdmin(harness);
		const picker = await screen.findByLabelText("attachment files for save"); expect(picker).toHaveAttribute("accept", "image/png"); await user.upload(picker, new File(["png"], "cover.png", { type: "image/png" }));
		expect(await screen.findByRole("img", { name: "Selected cover.png" })).toHaveAttribute("src", "blob:selected-image"); expect(callsTo(harness.script, "createWithFiles")).toHaveLength(0);
	});

	it("atomically attaches selected files while preserving the edit precondition", async () => {
		const harness = scriptedAdmin(`/admin/collections/documents/records/${recordID}`);
		harness.script.catalog.push({ value: { version: 4, collections: [documents] } });
		harness.script.read.push({ value: { data: { id: recordID, title: "contract" }, etag: '"pgb1.before"' } });
		harness.script.listFiles.push({ value: [] });
		harness.script.updateWithFiles.push({ value: { data: { id: recordID, title: "contract" }, etag: '"pgb1.after"' } });
		const user = userEvent.setup(); renderAdmin(harness);
		await user.upload(await screen.findByLabelText("attachment files for save"), new File(["revision"], "revision.txt", { type: "text/plain" }));
		await user.click(screen.getByRole("button", { name: "Save changes" }));
		await waitFor(() => expect(callsTo(harness.script, "updateWithFiles")).toHaveLength(1));
		expect(callsTo(harness.script, "updateWithFiles")[0].detail).toMatchObject({ id: recordID, patch: {}, files: { attachment: ["revision.txt"] }, etag: '"pgb1.before"' });
	});

  it("duplicates into a fresh create attempt without copying system identity", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts, tagsCollection] } }, { value: { version: 4, collections: [posts, tagsCollection] } });
    harness.script.read.push({ value: { data: { id: recordID, created_at: "old", title: "copy me", tags: [] }, etag: '"pgb1.one"' } });
    harness.script.create.push({ value: { data: { id: otherTargetID, title: "copy me", tags: [] }, etag: '"pgb1.new"' } });
    const user = userEvent.setup(); renderAdmin(harness);
    await user.click(await screen.findByRole("button", { name: "Duplicate record" }));
    expect(await screen.findByRole("heading", { name: "Create posts record" })).toBeVisible();
    expect(screen.getByLabelText("title")).toHaveValue("copy me");
    await user.click(screen.getByRole("button", { name: "Create record" }));
    await waitFor(() => expect(callsTo(harness.script, "create")).toHaveLength(1));
    expect(callsTo(harness.script, "create")[0].detail.input).toEqual({ title: "copy me", tags: [] });
  });

  it("renders allowed text and JSON values as constrained selectors", async () => {
    const constrained = collection("settings", "settings-id", [
      textField("theme", 0, { options: { allowedValues: ["light", "dark"] } }),
      { name: "channels", type: "jsonb", required: false, unique: false, indexed: false, ordinal: 1, options: { allowedValues: ["email", "sms"] } },
    ]);
    const harness = scriptedAdmin("/admin/collections/settings/records/new");
    harness.script.catalog.push({ value: { version: 4, collections: [constrained] } });
    const user = userEvent.setup(); renderAdmin(harness);
    await user.selectOptions(await screen.findByLabelText("theme state"), "value");
    await user.selectOptions(screen.getByLabelText("channels state"), "value");
    expect(screen.getByRole("combobox", { name: "theme" })).toBeVisible();
    expect(screen.getByRole("listbox", { name: "channels" })).toHaveAttribute("multiple");
  });
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

  it("creates a relation target inline through the normal record create contract", async () => {
    const harness = scriptedAdmin(`/admin/collections/posts/records/${recordID}`);
    harness.script.catalog.push({ value: { version: 4, collections: [posts, tagsCollection] } });
    harness.script.read.push({ value: { data: { id: recordID, title: "loaded", tags: [] }, etag: '"pgb1.one"' } });
    harness.script.create.push({ value: { data: { id: targetID, label: "new tag" }, etag: '"pgb1.tag"' } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.click(await screen.findByText("Create tags inline"));
    await user.type(screen.getByLabelText("New tags label"), "new tag");
    await user.click(screen.getByRole("button", { name: "Create and select" }));

    await waitFor(() => expect(screen.getByLabelText("tags")).toHaveValue(targetID));
    expect(callsTo(harness.script, "create")[0].detail).toMatchObject({ collection: "tags", input: { label: "new tag" } });
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
