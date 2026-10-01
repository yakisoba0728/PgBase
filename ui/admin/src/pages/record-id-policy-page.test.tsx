import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { FieldDefinition } from "../backend/ports";
import { callsTo } from "../test/backend-script";
import { administrator, collection, renderAdmin, scriptedAdmin, textField } from "../test/harness";
import { readDraft, writeDraft } from "../workspace/record-productivity";

describe("record identity administration", () => {
  it("previews the chosen exact text identity policy and invalidates it when the policy changes", async () => {
    const harness = scriptedAdmin("/admin/collections/new");
    harness.script.plan.push({ value: { baseVersion: 4, checksum: "text-policy", fields: [], sql: ["CREATE TABLE app.notes (...)"] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    await user.type(await screen.findByLabelText("Collection name"), "notes");
    await user.type(screen.getByLabelText("Field 1 name"), "title");
    await user.selectOptions(screen.getByLabelText("Record ID kind"), "text");
    await user.clear(screen.getByLabelText("Record ID minimum length"));
    await user.type(screen.getByLabelText("Record ID minimum length"), "1");
    await user.clear(screen.getByLabelText("Record ID maximum length"));
    await user.type(screen.getByLabelText("Record ID maximum length"), "32");
    await user.clear(screen.getByLabelText("Record ID pattern"));
    fireEvent.change(screen.getByLabelText("Record ID pattern"), { target: { value: "^[A-Za-z0-9]+$" } });
    await user.clear(screen.getByLabelText("Record ID generation pattern"));
    await user.click(screen.getByRole("button", { name: "Preview collection" }));

    expect(await screen.findByRole("button", { name: "Apply collection" })).toBeEnabled();
    expect(callsTo(harness.script, "plan")[0].detail.change).toMatchObject({
      recordId: { kind: "text", minLength: 1, maxLength: 32, pattern: "^[A-Za-z0-9]+$", autogeneratePattern: "" },
    });
    await user.clear(screen.getByLabelText("Record ID maximum length"));
    await user.type(screen.getByLabelText("Record ID maximum length"), "40");
    await waitFor(() => expect(screen.queryByRole("button", { name: "Apply collection" })).not.toBeInTheDocument());
  });

  it("creates a record with an exact mixed-case ID under the server's text policy", async () => {
    const notes = { ...collection("notes", "notes-id", [textField("title", 0)]), recordId: {
      kind: "text" as const, minLength: 1, maxLength: 32, pattern: "(?i)^[a-z0-9]+$",
    } };
    const harness = scriptedAdmin("/admin/collections/notes/records/new");
    harness.script.catalog.push({ value: { version: 4, collections: [notes] } });
    harness.script.create.push({ value: { data: { id: "MiXeDNote01" }, etag: '"pgb1.text"' } });
    harness.script.catalog.push({ value: { version: 4, collections: [notes] } });
    harness.script.list.push({ value: { items: [] } });
    const user = userEvent.setup();
    renderAdmin(harness);

    const input = await screen.findByLabelText("Record ID");
    expect(input).toBeRequired();
    await user.type(input, "MiXeDNote01");
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(callsTo(harness.script, "create")).toHaveLength(1));
    expect(callsTo(harness.script, "create")[0].detail.input).toEqual({ id: "MiXeDNote01" });
    expect(await screen.findByRole("heading", { name: "notes records" })).toBeVisible();
  });

  it("leaves an optional ID out of creation so the server can generate it", async () => {
    const notes = { ...collection("notes", "notes-id"), recordId: {
      kind: "text" as const, minLength: 1, maxLength: 32, pattern: "^[A-Za-z0-9]+$", autogeneratePattern: "[a-z]{15}",
    } };
    const harness = scriptedAdmin("/admin/collections/notes/records/new");
    harness.script.catalog.push({ value: { version: 4, collections: [notes] } });
    harness.script.create.push({ value: { data: { id: "generatedrecord" }, etag: '"pgb1.text"' } });
    const user = userEvent.setup(); renderAdmin(harness);

    expect(await screen.findByLabelText("Record ID")).not.toBeRequired();
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(callsTo(harness.script, "create")).toHaveLength(1));
    expect(callsTo(harness.script, "create")[0].detail.input).toEqual({});
  });

  it("keeps a rejected ID editable and sends the corrected exact value", async () => {
    const notes = { ...collection("notes", "notes-id"), recordId: { kind: "text" as const, minLength: 1, maxLength: 32, pattern: "^[A-Za-z0-9]+$" } };
    const harness = scriptedAdmin("/admin/collections/notes/records/new");
    harness.script.catalog.push({ value: { version: 4, collections: [notes] } });
    harness.script.create.push({ fault: { kind: "rejected", details: { id: ["record_id_conflict"] } } });
    harness.script.create.push({ value: { data: { id: "NewID" }, etag: '"pgb1.text"' } });
    const user = userEvent.setup(); renderAdmin(harness);

    await user.type(await screen.findByLabelText("Record ID"), "ExistingID");
    await user.click(screen.getByRole("button", { name: "Create record" }));
    expect(await screen.findByText("This record ID is already in use.")).toBeVisible();
    await user.clear(screen.getByLabelText("Record ID"));
    await user.type(screen.getByLabelText("Record ID"), "NewID");
    await user.click(screen.getByRole("button", { name: "Create record" }));

    await waitFor(() => expect(callsTo(harness.script, "create")).toHaveLength(2));
    expect(callsTo(harness.script, "create")[1].detail.input).toEqual({ id: "NewID" });
  });

  it("uses the target collection's text policy when editing a relation", async () => {
    const related: FieldDefinition = { name: "tags", type: "relation", ordinal: 0, required: false, unique: false, indexed: false, relation: { targetCollectionId: "tags-id", cardinality: "multiple", minSelect: 0, maxSelect: 4, onDelete: "restrict" } };
    const notes = collection("notes", "notes-id", [related]);
    const tags = { ...collection("tags", "tags-id"), recordId: { kind: "text" as const, minLength: 1, maxLength: 40, pattern: "^[A-Za-z0-9-]+$" } };
    const uuidShapedText = "AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE";
    const harness = scriptedAdmin("/admin/collections/notes/records/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee");
    harness.script.catalog.push({ value: { version: 4, collections: [notes, tags] } });
    harness.script.read.push({ value: { data: { id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", tags: [uuidShapedText, "FirstTag"] }, etag: '"pgb1.text"' } });
    harness.script.list.push({ value: { items: [{ id: uuidShapedText }, { id: "FirstTag" }] } });
    harness.script.update.push({ value: { data: { id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", tags: [uuidShapedText, "SecondTag"] }, etag: '"pgb1.updated"' } });
    const user = userEvent.setup(); renderAdmin(harness);

    expect(await screen.findByLabelText("tags")).toHaveValue(`${uuidShapedText}\nFirstTag`);
    await user.clear(screen.getByLabelText("tags"));
    await user.type(screen.getByLabelText("tags"), `${uuidShapedText}\nSecondTag`);
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(callsTo(harness.script, "update")).toHaveLength(1));
    expect(callsTo(harness.script, "update")[0].detail.patch).toEqual({ tags: [uuidShapedText, "SecondTag"] });
  });

  it("keeps the creation draft separate from an existing text record named new", async () => {
    const notes = { ...collection("notes", "notes-id", [textField("title", 0)]), recordId: { kind: "text" as const, minLength: 1, maxLength: 32, pattern: "^[A-Za-z0-9]+$" } };
    const createScope = { administrator: administrator.id, collection: "notes", record: "new" };
    const creationDraft = { title: { kind: "value" as const, value: "unsaved creation" } };
    writeDraft(createScope, creationDraft);
    const harness = scriptedAdmin("/admin/collections/notes/records/%6Eew");
    harness.script.catalog.push({ value: { version: 4, collections: [notes] } });
    harness.script.read.push({ value: { data: { id: "new", title: "stored record" }, etag: '"pgb1.text"' } });
    renderAdmin(harness);

    expect(await screen.findByLabelText("title")).toHaveValue("stored record");
    expect(readDraft(createScope)).toEqual(creationDraft);
  });

  it("shows the server's invalid related text ID error even without an editable record ID", async () => {
    const related: FieldDefinition = { name: "tag", type: "relation", ordinal: 0, required: false, unique: false, indexed: false, relation: { targetCollectionId: "tags-id", cardinality: "single", minSelect: 0, maxSelect: 1, onDelete: "restrict" } };
    const notes = collection("notes", "notes-id", [related]);
    const tags = { ...collection("tags", "tags-id"), recordId: { kind: "text" as const, minLength: 1, maxLength: 32, pattern: "^AllowedOnly$" } };
    const harness = scriptedAdmin("/admin/collections/notes/records/new");
    harness.script.catalog.push({ value: { version: 4, collections: [notes, tags] } });
    harness.script.create.push({ fault: { kind: "rejected", details: { id: ["invalid_record_id"] } } });
    const user = userEvent.setup(); renderAdmin(harness);
    await user.selectOptions(await screen.findByLabelText("tag state"), "value");
    await user.type(screen.getByLabelText("tag"), "RejectedByServerPolicy");
    await user.click(screen.getByRole("button", { name: "Create record" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("A record ID is invalid. Check the record ID and any related record IDs against their collection policies.");
    expect(screen.getByLabelText("tag")).toHaveValue("RejectedByServerPolicy");
  });
});
