import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useRecordEditor, useRecordList, type RecordEditor } from "./record-workspace";
import type { AdminRoute } from "../routing/admin-location";
import { UnsavedDrafts, draftLedger } from "../routing/unsaved-draft";
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

// The editor holds its draft in the same ledger the shell mounts, so a test that
// asks whether a draft survived can read the ledger rather than infer it.
function openEditor(id: string | undefined, collections: CollectionDefinition[] = [posts]) {
  const script = signedInScript();
  const navigate = vi.fn<(route: AdminRoute) => void>();
  const ledger = draftLedger(() => true);
  script.catalog.push({ value: { version: 4, collections } });
  const Session = wrap(script);
  const view = renderHook(
    ({ current }: { current: { collection: string; id?: string } }) => useRecordEditor(current.collection, current.id, navigate),
    {
      initialProps: { current: { collection: "posts", id } },
      wrapper: ({ children }: { children: ReactNode }) => <Session><UnsavedDrafts ledger={ledger}>{children}</UnsavedDrafts></Session>,
    },
  );
  return { script, navigate, ledger, ...view };
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

  it("walks Previous and Next along one cursor chain under one query", async () => {
    const { result, script } = openList([posts], { items: [{ id: recordID }], nextCursor: "unfiltered" });
    await waitFor(() => expect(result.current.page?.nextCursor).toBe("unfiltered"));

    script.list.push({ value: { items: [{ id: recordID }], nextCursor: "cursor one" } });
    act(() => result.current.setFilterText("title = 'a'"));
    act(() => result.current.applyFilter());
    await waitFor(() => expect(result.current.page?.nextCursor).toBe("cursor one"));
    expect(result.current.hasPrevious).toBe(false);
    act(() => result.current.previousPage());
    expect(callsTo(script, "list")).toHaveLength(2);

    script.list.push({ value: { items: [{ id: otherID }], nextCursor: "cursor two" } });
    act(() => result.current.nextPage());
    await waitFor(() => expect(result.current.page?.nextCursor).toBe("cursor two"));
    expect(result.current.hasPrevious).toBe(true);

    script.list.push({ value: { items: [{ id: targetID }] } });
    act(() => result.current.nextPage());
    await waitFor(() => expect(result.current.page?.nextCursor).toBeUndefined());

    script.list.push({ value: { items: [{ id: otherID }], nextCursor: "cursor two" } });
    act(() => result.current.previousPage());
    await waitFor(() => expect(result.current.page?.nextCursor).toBe("cursor two"));

    script.list.push({ value: { items: [{ id: recordID }], nextCursor: "cursor one" } });
    act(() => result.current.previousPage());
    await waitFor(() => expect(result.current.hasPrevious).toBe(false));

    expect(callsTo(script, "list").map((call) => call.detail)).toEqual([
      { collection: "posts" },
      { collection: "posts", filter: "title = 'a'" },
      { collection: "posts", cursor: "cursor one", filter: "title = 'a'" },
      { collection: "posts", cursor: "cursor two", filter: "title = 'a'" },
      { collection: "posts", cursor: "cursor one", filter: "title = 'a'" },
      { collection: "posts", filter: "title = 'a'" },
    ]);
  });

  it("forgets the cursor chain when the query changes", async () => {
    const { result, script } = openList([posts], { items: [{ id: recordID }], nextCursor: "cursor one" });
    await waitFor(() => expect(result.current.page).not.toBeNull());

    script.list.push({ value: { items: [{ id: otherID }] } });
    act(() => result.current.nextPage());
    await waitFor(() => expect(result.current.hasPrevious).toBe(true));

    // The reload the new sort starts has not landed yet, and the chain's cursors
    // address the old query's pages, so Previous is already unavailable.
    const sorted = deferredStep<RecordPage>();
    script.list.push(sorted.step);
    act(() => result.current.toggleSort("id"));
    expect(result.current.hasPrevious).toBe(false);
    act(() => result.current.previousPage());
    expect(callsTo(script, "list")).toHaveLength(3);

    await act(async () => { sorted.settle({ value: { items: [{ id: recordID }], nextCursor: "cursor three" } }); });
    expect(result.current.hasPrevious).toBe(false);
    expect(callsTo(script, "list").at(-1)?.detail).toEqual({ collection: "posts", sort: "id" });
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
  it("deletes selected records with their own ETags and keeps partial failures selected", async () => {
    const { result, script } = openList([posts], { items: [{ id: recordID }, { id: otherID }] });
    await waitFor(() => expect(result.current.page).not.toBeNull());
    script.read.push(
      { value: { data: { id: recordID }, etag: '"pgb1.first"' } },
      { value: { data: { id: otherID }, etag: '"pgb1.second"' } },
    );
    script.remove.push({ value: undefined }, { fault: { kind: "stale" } });
    script.list.push({ value: { items: [{ id: otherID }] } });
    vi.spyOn(window, "confirm").mockReturnValue(true);

    act(() => result.current.selectMany([recordID, otherID], true));
    act(() => result.current.deleteSelected());

    await waitFor(() => expect(result.current.actionMessage).toContain("Deleted 1 of 2"));
    expect([...result.current.selected]).toEqual([otherID]);
    expect(callsTo(script, "remove").map((call) => call.detail)).toEqual([
      { collection: "posts", id: recordID, etag: '"pgb1.first"' },
      { collection: "posts", id: otherID, etag: '"pgb1.second"' },
    ]);
  });

  it("exports every page under the active filter and sort with a clear filtered scope", async () => {
    const { result, script } = openList([posts], { items: [{ id: recordID }] });
    await waitFor(() => expect(result.current.page).not.toBeNull());
    script.list.push({ value: { items: [{ id: recordID }], nextCursor: "export-next" } });
    script.list.push({ value: { items: [{ id: otherID }] } });
    const createURL = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:records");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);

    act(() => result.current.exportJSON());

    await waitFor(() => expect(result.current.actionMessage).toBe("Exported 2 filtered records."));
    expect(createURL).toHaveBeenCalledOnce();
    expect(callsTo(script, "list").slice(-2).map((call) => call.detail)).toEqual([
      { collection: "posts" },
      { collection: "posts", cursor: "export-next" },
    ]);
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
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "my draft" });
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

describe("RecordEditor drafting during a save", () => {
  it("keeps a draft edited while the save was in flight instead of leaving as if it were saved", async () => {
    const { result, script, navigate, ledger } = openEditor(recordID);
    script.read.push({ value: { data: { id: recordID, title: "first" }, etag: '"pgb1.one"' } });
    await loaded(result);

    const saving = deferredStep<{ data: Record<string, string>; etag: string }>();
    script.update.push(saving.step);
    act(() => result.current.setValue(result.current.fields[0], "first draft"));
    act(() => result.current.save());

    act(() => result.current.setValue(result.current.fields[0], "new_unsaved_title"));
    await act(async () => { saving.settle({ value: { data: { id: recordID, title: "first draft" }, etag: '"pgb1.two"' } }); });

    expect(navigate).not.toHaveBeenCalled();
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "new_unsaved_title" });
    // The newer draft is unsaved work, so the departure guard still holds it and
    // the page says the sent save landed without claiming this one did.
    expect(ledger.held()).toBe(true);
    expect(result.current.message).toBe(
      "The record was saved. The edits you made after sending it are not saved yet — save again to store them, or leave this page to discard them.",
    );
    expect(result.current.pending).toBe(false);

    // The save that landed produced a new version, so the next one must carry it
    // rather than the version this editor loaded.
    script.update.push({ value: { data: { id: recordID, title: "new_unsaved_title" }, etag: '"pgb1.three"' } });
    await act(async () => { result.current.save(); });
    expect(callsTo(script, "update").at(-1)?.detail).toMatchObject({ etag: '"pgb1.two"', patch: { title: "new_unsaved_title" } });
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ kind: "records", collection: "posts" }));
    expect(ledger.held()).toBe(false);
  });

  it("reports a created record without treating the edits that followed as part of it", async () => {
    const { result, script, navigate, ledger } = openEditor(undefined);
    await loaded(result);

    const creating = deferredStep<{ data: Record<string, string>; etag: string }>();
    script.create.push(creating.step);
    act(() => result.current.setKind(result.current.fields[0], "value"));
    act(() => result.current.setValue(result.current.fields[0], "First"));
    act(() => result.current.save());

    act(() => result.current.setValue(result.current.fields[0], "new_unsaved_title"));
    await act(async () => { creating.settle({ value: { data: { id: recordID, title: "First" }, etag: '"pgb1.new"' } }); });

    expect(navigate).not.toHaveBeenCalled();
    expect(result.current.stateOf(result.current.fields[0])).toEqual({ kind: "value", value: "new_unsaved_title" });
    expect(ledger.held()).toBe(true);
    expect(result.current.message).toBe(
      "The record was created. The edits you made after sending it are not part of it — saving again creates another record.",
    );
  });
});
