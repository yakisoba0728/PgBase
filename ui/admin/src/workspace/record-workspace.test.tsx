import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useRecordEditor, useRecordList, useRelationPicker, type RecordEditor } from "./record-workspace";
import type { AdminRoute } from "../routing/admin-location";
import type { CollectionDefinition, FieldDefinition, RecordPage } from "../backend/ports";
import { callsTo, deferredStep, type BackendScript } from "../test/backend-script";
import { collection, sessionWrapper, signedInScript, textField } from "../test/harness";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";

const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const otherID = "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff";
const targetID = "11111111-1111-4111-8111-111111111111";

const posts = collection("posts", "posts-id", [textField("title", 0)]);
const users = collection("users", "users-id", [textField("name", 0, { indexed: true })]);

function relationField(name: string, cardinality: "single" | "multiple", patch: Partial<NonNullable<FieldDefinition["relation"]>> = {}): FieldDefinition {
  return {
    name, type: "relation", required: false, unique: false, indexed: true, ordinal: 1,
    relation: { targetCollectionId: "users-id", cardinality, minSelect: 0, maxSelect: cardinality === "single" ? 1 : 3, onDelete: "restrict", ...patch },
  };
}

function wrap(script: BackendScript) {
  return sessionWrapper(memoryBackend(script), memoryHistory());
}

function openList(collections: CollectionDefinition[] = [posts], first: RecordPage = { items: [{ id: recordID }] }) {
  const script = signedInScript();
  script.catalog.push({ value: { version: 4, collections } });
  script.list.push({ value: first });
  const view = renderHook(({ name }: { name: string }) => useRecordList(name), { initialProps: { name: "posts" }, wrapper: wrap(script) });
  return { script, ...view };
}

function openEditor(id: string | undefined, collections: CollectionDefinition[] = [posts]) {
  const script = signedInScript();
  const navigate = vi.fn<(route: AdminRoute) => void>();
  script.catalog.push({ value: { version: 4, collections } });
  const view = renderHook(
    ({ current }: { current: { collection: string; id?: string } }) => useRecordEditor(current.collection, current.id, navigate),
    { initialProps: { current: { collection: "posts", id } }, wrapper: wrap(script) },
  );
  return { script, navigate, ...view };
}

async function loaded(result: { current: RecordEditor }): Promise<void> {
  await waitFor(() => expect(result.current.collection).not.toBeNull());
}

afterEach(() => vi.restoreAllMocks());

describe("RecordList", () => {
  it("carries the applied filter and sort into every page including the next cursor", async () => {
    const { result, script } = openList([posts], { items: [{ id: recordID }], nextCursor: "signed cursor" });
    await waitFor(() => expect(result.current.page).not.toBeNull());

    script.list.push({ value: { items: [{ id: recordID }] } });
    act(() => result.current.setFilterText("  title = 'first' "));
    act(() => result.current.applyFilter());
    await waitFor(() => expect(result.current.page?.nextCursor).toBeUndefined());

    script.list.push({ value: { items: [{ id: recordID }], nextCursor: "page two" } });
    act(() => result.current.toggleSort("title"));
    await waitFor(() => expect(result.current.page?.nextCursor).toBe("page two"));
    expect(result.current.sortDirection("title")).toBe("ascending");

    script.list.push({ value: { items: [{ id: otherID }] } });
    act(() => result.current.nextPage());
    await waitFor(() => expect(callsTo(script, "list")).toHaveLength(4));
    expect(callsTo(script, "list")[3].detail).toEqual({ collection: "posts", cursor: "page two", filter: "title = 'first'", sort: "title" });
  });

  it("cycles a sortable column through ascending, descending and unsorted", async () => {
    const { result, script } = openList([posts], { items: [] });
    await waitFor(() => expect(result.current.page).not.toBeNull());

    for (const expected of ["title", "-title", ""]) {
      script.list.push({ value: { items: [] } });
      act(() => result.current.toggleSort("title"));
      await waitFor(() => expect(callsTo(script, "list").at(-1)?.detail.sort ?? "").toBe(expected));
    }
  });

  it("offers sorting only for columns the server can order on", async () => {
    const sortable = collection("posts", "posts-id", [
      textField("title", 0, { indexed: true }),
      textField("body", 1, { indexed: false }),
      { name: "metadata", type: "jsonb", required: false, unique: false, indexed: true, ordinal: 2 },
      relationField("owner", "single"),
      { ...relationField("tags", "multiple"), ordinal: 4 },
    ]);
    const { result } = openList([sortable], { items: [] });
    await waitFor(() => expect(result.current.columns.length).toBeGreaterThan(0));

    expect(result.current.sortable("id")).toBe(true);
    expect(result.current.sortable("created_at")).toBe(true);
    expect(result.current.sortable("title")).toBe(true);
    expect(result.current.sortable("body")).toBe(false);
    expect(result.current.sortable("metadata")).toBe(false);
    expect(result.current.sortable("owner")).toBe(true);
    expect(result.current.sortable("tags")).toBe(false);
  });

  it("reports a rejected filter and clears the report after a good page", async () => {
    const { result, script } = openList();
    await waitFor(() => expect(result.current.page).not.toBeNull());

    script.list.push({ fault: { kind: "rejected", details: { filter: ["unknown_field"] } } });
    act(() => result.current.setFilterText("ghost = 1"));
    act(() => result.current.applyFilter());
    await waitFor(() => expect(result.current.filterInvalid).toBe(true));
    expect(result.current.failed).toBe(false);

    script.list.push({ value: { items: [{ id: recordID }] } });
    act(() => result.current.clear());
    await waitFor(() => expect(result.current.filterInvalid).toBe(false));
    expect(result.current.filterText).toBe("");
  });

  it("resets definition, page, filter, sort and cursor when the collection changes", async () => {
    const { result, script, rerender } = openList([posts, collection("users", "users-id", [textField("name", 0)])], { items: [{ id: recordID }], nextCursor: "signed cursor" });
    await waitFor(() => expect(result.current.page).not.toBeNull());
    script.list.push({ value: { items: [{ id: recordID }], nextCursor: "signed cursor" } });
    act(() => result.current.setFilterText("title = 'x'"));
    act(() => result.current.applyFilter());
    act(() => result.current.toggleSort("title"));
    await waitFor(() => expect(result.current.filtered).toBe(true));

    script.catalog.push({ value: { version: 4, collections: [posts, collection("users", "users-id", [textField("name", 0)])] } });
    script.list.push({ value: { items: [{ id: otherID }] } });
    rerender({ name: "users" });

    expect(result.current.collection).toBeNull();
    expect(result.current.page).toBeNull();
    expect(result.current.filterText).toBe("");
    expect(result.current.filtered).toBe(false);
    expect(result.current.sortDirection("title")).toBeUndefined();
    await waitFor(() => expect(result.current.collection?.name).toBe("users"));
    expect(callsTo(script, "list").at(-1)?.detail).toEqual({ collection: "users" });
  });

  it("reports a collection the catalog does not have as a failed list", async () => {
    const script = signedInScript();
    script.catalog.push({ value: { version: 4, collections: [] } });
    script.list.push({ fault: { kind: "not-found" } });
    const { result } = renderHook(() => useRecordList("constructor"), { wrapper: wrap(script) });

    await waitFor(() => expect(result.current.failed).toBe(true));
    expect(result.current.collection).toBeNull();
  });
});

describe("RecordEditor", () => {
  it("creates a record under the collection's own attempt and returns to the list", async () => {
    const { result, script, navigate } = openEditor(undefined);
    await loaded(result);

    script.create.push({ value: { data: { id: recordID, title: "First" }, etag: '"pgb1.new"' } });
    act(() => result.current.setKind(result.current.fields[0], "value"));
    act(() => result.current.setValue(result.current.fields[0], "First"));
    act(() => result.current.save());

    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ kind: "records", collection: "posts" }));
    expect(callsTo(script, "create")[0].detail).toMatchObject({ collection: "posts", input: { title: "First" } });
  });

  it("retries an ambiguous create with the same idempotency key and rotates it when the draft changes", async () => {
    const { result, script } = openEditor(undefined);
    await loaded(result);

    script.create.push({ fault: { kind: "unreachable" } });
    act(() => result.current.setKind(result.current.fields[0], "value"));
    act(() => result.current.setValue(result.current.fields[0], "First"));
    act(() => result.current.save());
    await waitFor(() => expect(result.current.message).toBe("Unable to save the record."));

    script.create.push({ fault: { kind: "unreachable" } });
    act(() => result.current.save());
    await waitFor(() => expect(callsTo(script, "create")).toHaveLength(2));
    const [firstTry, retry] = callsTo(script, "create");
    expect(retry.detail.key).toBe(firstTry.detail.key);

    script.create.push({ fault: { kind: "unreachable" } });
    act(() => result.current.setValue(result.current.fields[0], "Second"));
    act(() => result.current.save());
    await waitFor(() => expect(callsTo(script, "create")).toHaveLength(3));
    expect(callsTo(script, "create")[2].detail.key).not.toBe(firstTry.detail.key);
  });

  it("sends only the changed fields in a patch and maps rejected codes to safe messages", async () => {
    const { result, script } = openEditor(recordID);
    script.read.push({ value: { data: { id: recordID, title: "old" }, etag: '"pgb1.one"' } });
    await loaded(result);

    script.update.push({ fault: { kind: "rejected", details: { title: ["value_too_long"] } } });
    act(() => result.current.setValue(result.current.fields[0], "new"));
    act(() => result.current.save());

    await waitFor(() => expect(result.current.errors.title).toBe("Value exceeds the 2,048-byte limit for text values."));
    expect(callsTo(script, "update")[0].detail).toMatchObject({ patch: { title: "new" }, etag: '"pgb1.one"' });
    expect(result.current.message).toBeNull();
  });

  it("keeps a stale draft and reloads the record only when asked", async () => {
    const { result, script } = openEditor(recordID);
    script.read.push({ value: { data: { id: recordID, title: "old" }, etag: '"pgb1.one"' } });
    await loaded(result);

    script.update.push({ fault: { kind: "stale" } });
    act(() => result.current.setValue(result.current.fields[0], "my draft"));
    act(() => result.current.save());
    await waitFor(() => expect(result.current.stale).toBe(true));
    expect(result.current.message).toContain("Reload the latest version");
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "my draft" });

    script.catalog.push({ value: { version: 4, collections: [posts] } });
    script.read.push({ fault: { kind: "unreachable" } });
    act(() => result.current.reload());
    await waitFor(() => expect(result.current.message).toBe("Unable to reload the latest record. Your draft was preserved; try again."));
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "my draft" });

    script.catalog.push({ value: { version: 4, collections: [posts] } });
    script.read.push({ value: { data: { id: recordID, title: "theirs" }, etag: '"pgb1.two"' } });
    act(() => result.current.reload());
    await waitFor(() => expect(result.current.stale).toBe(false));
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "theirs" });
  });

  it("reports a record another administrator already deleted", async () => {
    const { result, script } = openEditor(recordID);
    script.read.push({ value: { data: { id: recordID, title: "old" }, etag: '"pgb1.one"' } });
    await loaded(result);
    vi.spyOn(window, "confirm").mockReturnValue(true);

    script.remove.push({ fault: { kind: "not-found" } });
    act(() => result.current.remove());
    await waitFor(() => expect(result.current.message).toBe("This record no longer exists."));
  });

  it("explains a delete another record still references", async () => {
    const { result, script } = openEditor(recordID);
    script.read.push({ value: { data: { id: recordID, title: "old" }, etag: '"pgb1.one"' } });
    await loaded(result);
    vi.spyOn(window, "confirm").mockReturnValue(true);

    script.remove.push({ fault: { kind: "rejected", details: { id: ["relation_delete_restricted"] } } });
    act(() => result.current.remove());
    await waitFor(() => expect(result.current.message).toBe("Other records still reference this record. Remove those references first."));
  });

  it("does not delete until the administrator confirms", async () => {
    const { result, script } = openEditor(recordID);
    script.read.push({ value: { data: { id: recordID }, etag: '"pgb1.one"' } });
    await loaded(result);
    vi.spyOn(window, "confirm").mockReturnValue(false);

    act(() => result.current.remove());
    expect(callsTo(script, "remove")).toHaveLength(0);
  });

  it("reports a record the catalog cannot place", async () => {
    const { result } = openEditor(recordID, []);
    await waitFor(() => expect(result.current.message).toBe("Unable to load the record editor."));
    expect(result.current.collection).toBeNull();
  });
});

describe("RecordEditor resource isolation", () => {
  it("never carries a new-record draft into an existing record", async () => {
    const { result, script, rerender } = openEditor(undefined);
    await loaded(result);
    act(() => result.current.setKind(result.current.fields[0], "value"));
    act(() => result.current.setValue(result.current.fields[0], "Unsaved new draft"));

    script.catalog.push({ value: { version: 4, collections: [posts] } });
    script.read.push({ value: { data: { id: recordID, title: "Existing record" }, etag: '"pgb1.one"' } });
    rerender({ current: { collection: "posts", id: recordID } });

    expect(result.current.collection).toBeNull();
    await loaded(result);
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "Existing record" });
  });

  it("drops a save result that arrives after the editor moved to another record", async () => {
    const { result, script, rerender, navigate } = openEditor(recordID);
    script.read.push({ value: { data: { id: recordID, title: "first" }, etag: '"pgb1.one"' } });
    await loaded(result);

    const update = deferredStep<{ data: Record<string, string>; etag: string }>();
    script.update.push(update.step);
    act(() => result.current.setValue(result.current.fields[0], "first draft"));
    act(() => result.current.save());

    script.catalog.push({ value: { version: 4, collections: [posts] } });
    script.read.push({ value: { data: { id: otherID, title: "second" }, etag: '"pgb1.two"' } });
    rerender({ current: { collection: "posts", id: otherID } });
    await loaded(result);

    await act(async () => { update.settle({ value: { data: { id: recordID, title: "first draft" }, etag: '"pgb1.updated"' } }); });

    expect(navigate).not.toHaveBeenCalled();
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "second" });
    expect(result.current.pending).toBe(false);
  });
});

describe("relation picker", () => {
  function openPicker(target: CollectionDefinition, field: FieldDefinition, draft = "") {
    const script = signedInScript();
    const onChange = vi.fn<(value: string) => void>();
    const view = renderHook(() => useRelationPicker(target, field, draft, onChange), { wrapper: wrap(script) });
    return { script, onChange, ...view };
  }

  it("searches only when asked and sends one escaped, indexed lookup", async () => {
    const field = relationField("owner", "single");
    const { result, script } = openPicker(users, field);

    act(() => result.current.setQuery("O'Hara"));
    expect(callsTo(script, "list")).toHaveLength(0);

    script.list.push({ value: { items: [{ id: targetID, name: "O'Hara" }] } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.results).toHaveLength(1));
    expect(callsTo(script, "list")[0].detail).toEqual({ collection: "users", filter: "name = 'O''Hara'", sort: "name" });
    expect(result.current.results?.[0]).toEqual({ id: targetID, label: "O'Hara" });
  });

  it("reports an empty result and a truncated result differently", async () => {
    const { result, script } = openPicker(users, relationField("owner", "single"));

    script.list.push({ value: { items: [] } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.status).toBe("No matching target records."));

    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "more" } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.status).toBe("Showing the first 50 matching target records."));
  });

  it("refuses a query the target cannot be looked up by", async () => {
    const { result, script } = openPicker(collection("tags", "tags-id"), relationField("tags", "multiple"));

    act(() => result.current.setQuery("not-a-uuid"));
    act(() => result.current.search());
    await waitFor(() => expect(result.current.status).toBe("Enter a canonical, non-zero target UUID to search."));
    expect(callsTo(script, "list")).toHaveLength(0);
  });

  it("reports a duplicate selection instead of changing the draft", async () => {
    const field = relationField("tags", "multiple");
    const { result, onChange } = openPicker(users, field, targetID);

    act(() => result.current.select(targetID));
    expect(onChange).not.toHaveBeenCalled();
    expect(result.current.status).toBe("That target is already selected.");
  });
});
