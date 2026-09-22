import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { CollectionDefinition, FieldDefinition, RecordPage } from "../backend/ports";
import { callsTo, deferredStep, type BackendScript, type ScriptedRecord } from "../test/backend-script";
import { collection, sessionWrapper, signedInScript, textField } from "../test/harness";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";
import { useRelationPicker } from "./relation-picker";
import { RelationTargetPicker } from "./relation-target-picker";

const targetID = "11111111-1111-4111-8111-111111111111";
const otherID = "22222222-2222-4222-8222-222222222222";
const thirdID = "33333333-3333-4333-8333-333333333333";

const users = collection("users", "users-id", [textField("name", 0, { indexed: true })]);

function bulkID(index: number): string {
  return `00000000-0000-4000-8000-${index.toString(16).padStart(12, "0")}`;
}

function relationField(name: string, cardinality: "single" | "multiple", patch: Partial<NonNullable<FieldDefinition["relation"]>> = {}): FieldDefinition {
  return {
    name, type: "relation", required: false, unique: false, indexed: true, ordinal: 1,
    relation: { targetCollectionId: "users-id", cardinality, minSelect: 0, maxSelect: cardinality === "single" ? 1 : 1000, onDelete: "restrict", ...patch },
  };
}

function wrap(script: BackendScript) {
  return sessionWrapper(memoryBackend(script), memoryHistory());
}

describe("relation picker", () => {
  function openPicker(target: CollectionDefinition, field: FieldDefinition, draft = "", hydration?: RecordPage) {
    const script = signedInScript();
    if (hydration !== undefined) script.list.push({ value: hydration });
    const onChange = vi.fn<(value: string) => void>();
    const view = renderHook(() => useRelationPicker(target, field, draft, onChange), { wrapper: wrap(script) });
    return { script, onChange, ...view };
  }

  it("searches, hydrates, selects and removes text targets without changing identity", async () => {
    const textUsers: CollectionDefinition = { ...users, fields: [], recordId: { kind: "text", minLength: 1, maxLength: 255, pattern: "(?i)^[a-z0-9]+$" } };
    const { result, script, onChange } = openPicker(textUsers, relationField("tags", "multiple"), "MixedCase01", { items: [{ id: "MixedCase01" }] });
    await waitFor(() => expect(callsTo(script, "list")).toHaveLength(1));
    expect(result.current.selections).toEqual([{ id: "MixedCase01", label: "MixedCase01", available: true }]);
    expect(callsTo(script, "list")[0].detail).toEqual({ collection: "users", filter: "id = 'MixedCase01'" });

    script.list.push({ value: { items: [{ id: "mixedcase01" }] } });
    act(() => result.current.setQuery("mixedcase01"));
    act(() => result.current.search());
    await waitFor(() => expect(result.current.results).toEqual([{ id: "mixedcase01", label: "mixedcase01" }]));
    expect(callsTo(script, "list")[1].detail).toEqual({ collection: "users", filter: "id = 'mixedcase01'" });
    act(() => result.current.select("mixedcase01"));
    expect(onChange).toHaveBeenLastCalledWith("MixedCase01\nmixedcase01");
    act(() => result.current.deselect("MixedCase01"));
    expect(onChange).toHaveBeenLastCalledWith("");
  });

  it("creates a text target inline with its explicit exact ID", async () => {
    const target: CollectionDefinition = { ...users, recordId: { kind: "text", minLength: 1, maxLength: 255, pattern: "^[A-Za-z0-9]+$" } };
    const script = signedInScript();
    script.create.push({ value: { data: { id: "MixedCase01", name: "Ada" }, etag: '"v1"' } });
    const onChange = vi.fn();
    const user = userEvent.setup();
    render(<RelationTargetPicker target={target} field={relationField("owner", "single")} draft="" disabled={false} onChange={onChange} />, { wrapper: wrap(script) });

    await user.click(screen.getByText("Create users inline"));
    await user.type(screen.getByLabelText("New users record ID"), "MixedCase01");
    await user.type(screen.getByLabelText("New users name"), "Ada");
    await user.click(screen.getByRole("button", { name: "Create and select" }));

    await waitFor(() => expect(onChange).toHaveBeenCalledWith("MixedCase01"));
    expect(callsTo(script, "create")[0].detail).toMatchObject({ collection: "users", input: { id: "MixedCase01", name: "Ada" } });
  });

  it("hydrates long text IDs within both the filter byte limit and the request budget", async () => {
    const target: CollectionDefinition = { ...users, recordId: { kind: "text", minLength: 1, maxLength: 255, pattern: "^.+$" } };
    const ids = Array.from({ length: 50 }, (_ignored, index) => `${"𐐀".repeat(250)}${index}`);
    const script = signedInScript();
    for (let index = 0; index < 5; index += 1) script.list.push({ value: { items: [] } });
    const view = renderHook(() => useRelationPicker(target, relationField("tags", "multiple"), ids.join("\n"), vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(callsTo(script, "list")).toHaveLength(4));
    await act(async () => { await new Promise((settle) => setTimeout(settle, 0)); });
    expect(callsTo(script, "list")).toHaveLength(4);
    for (const call of callsTo(script, "list")) {
      const filter = (call.detail as { filter: string }).filter;
      expect(new TextEncoder().encode(filter).length).toBeLessThanOrEqual(4096);
    }
    expect(view.result.current.selections.at(-1)).toEqual({ id: ids.at(-1), label: ids.at(-1), available: true });
  });

  it("keeps hydrating the current text selection after earlier edits used four batches", async () => {
    const target: CollectionDefinition = { ...users, recordId: { kind: "text", minLength: 1, maxLength: 255, pattern: "^[A-Za-z]+$" } };
    const script = signedInScript();
    const ids = ["A", "Al", "Ali", "Alic", "Alice"];
    for (const id of ids) script.list.push({ value: { items: [{ id, name: "Alice Smith" }] } });
    const view = renderHook(({ draft }) => useRelationPicker(target, relationField("owner", "single"), draft, vi.fn()), { initialProps: { draft: "" }, wrapper: wrap(script) });

    for (const [index, draft] of ids.entries()) {
      view.rerender({ draft });
      await waitFor(() => expect(callsTo(script, "list")).toHaveLength(index + 1));
      await waitFor(() => expect(view.result.current.selections[0].label).toBe("Alice Smith"));
    }
    view.rerender({ draft: "Alice" });
    await act(async () => { await new Promise((settle) => setTimeout(settle, 0)); });
    expect(callsTo(script, "list")).toHaveLength(5);
  });

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

  it("browses the target in its indexed order when the query is empty", async () => {
    const { result, script } = openPicker(users, relationField("owner", "single"));

    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }] } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.results).toHaveLength(1));
    expect(callsTo(script, "list")[0].detail).toEqual({ collection: "users", sort: "name" });
  });

  it("reports an empty result and a truncated result differently", async () => {
    const { result, script } = openPicker(users, relationField("owner", "single"));

    script.list.push({ value: { items: [] } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.status).toBe("No matching target records."));
    expect(result.current.hasMore).toBe(false);

    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "more" } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.status).toBe("Showing 1 target record. Load more to see the rest."));
    expect(result.current.hasMore).toBe(true);
  });

  it("loads the next page through the cursor the open plan was issued under", async () => {
    const { result, script } = openPicker(users, relationField("owner", "single"));

    act(() => result.current.setQuery("Ada"));
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "page two" } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.hasMore).toBe(true));

    // Retyping the box must not retarget a cursor the server issued for the
    // query that is still on screen.
    act(() => result.current.setQuery("Grace"));
    script.list.push({ value: { items: [{ id: otherID, name: "Ada Two" }] } });
    act(() => result.current.loadMore());
    await waitFor(() => expect(result.current.results).toHaveLength(2));

    expect(callsTo(script, "list")[1].detail).toEqual({ collection: "users", cursor: "page two", filter: "name = 'Ada'", sort: "name" });
    expect(result.current.results?.map((option) => option.id)).toEqual([targetID, otherID]);
    expect(result.current.hasMore).toBe(false);
  });

  it("hydrates the labels the draft's ids already stand for in one bounded lookup", async () => {
    const script = signedInScript();
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }, { id: otherID, name: "Grace" }] } });
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), `${targetID}\n${otherID}`, vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(view.result.current.selections[0].label).toBe("Ada"));
    expect(callsTo(script, "list")).toHaveLength(1);
    expect(callsTo(script, "list")[0].detail).toEqual({ collection: "users", filter: `id = '${targetID}' || id = '${otherID}'` });
    expect(view.result.current.selections).toEqual([
      { id: targetID, label: "Ada", available: true },
      { id: otherID, label: "Grace", available: true },
    ]);
  });

  it("continues selected-target hydration after a byte-truncated page", async () => {
    const script = signedInScript();
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "selected page two" } });
    script.list.push({ value: { items: [{ id: otherID, name: "Grace" }] } });
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), `${targetID}\n${otherID}`, vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(view.result.current.selections[1].label).toBe("Grace"));
    expect(view.result.current.selections).toEqual([
      { id: targetID, label: "Ada", available: true },
      { id: otherID, label: "Grace", available: true },
    ]);
    expect(callsTo(script, "list")[1].detail).toEqual({
      collection: "users", cursor: "selected page two", filter: `id = '${targetID}' || id = '${otherID}'`,
    });
  });

  it("leaves an unresolved target available when hydration exhausts its page budget", async () => {
    const script = signedInScript();
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "page two" } });
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "page three" } });
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), `${targetID}\n${otherID}`, vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(callsTo(script, "list")).toHaveLength(2));
    await waitFor(() => expect(view.result.current.selections[0].label).toBe("Ada"));
    expect(view.result.current.selections[1]).toEqual({ id: otherID, label: otherID, available: true });
  });

  it("leaves an unresolved target available when a later hydration page fails", async () => {
    const script = signedInScript();
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "page two" } });
    script.list.push({ fault: { kind: "unreachable" } });
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), `${targetID}\n${otherID}`, vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(callsTo(script, "list")).toHaveLength(2));
    await act(async () => { await new Promise((settle) => setTimeout(settle, 0)); });
    expect(view.result.current.selections[1]).toEqual({ id: otherID, label: otherID, available: true });
  });

  it("does not request another hydration page after its editor unmounts", async () => {
    const script = signedInScript();
    const firstPage = deferredStep<RecordPage>();
    script.list.push(firstPage.step);
    const backend = memoryBackend(script);
    const listIgnoringAbort = vi.fn((collection: string, options: Parameters<typeof backend.records.list>[1]) =>
      backend.records.list(collection, options));
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), `${targetID}\n${otherID}`, vi.fn()), {
      wrapper: sessionWrapper({ ...backend, records: { ...backend.records, list: listIgnoringAbort } }, memoryHistory()),
    });
    await waitFor(() => expect(listIgnoringAbort).toHaveBeenCalledTimes(1));

    view.unmount();
    await act(async () => { firstPage.settle({ value: { items: [{ id: targetID, name: "Ada" }], nextCursor: "page two" } }); });
    expect(listIgnoringAbort).toHaveBeenCalledTimes(1);
  });

  it("does not patch an inline target after its picker unmounts", async () => {
    const script = signedInScript();
    const loaded = deferredStep<ScriptedRecord>();
    script.read.push(loaded.step);
    const view = renderHook(
      () => useRelationPicker(users, relationField("owner", "single"), "", vi.fn()),
      { wrapper: wrap(script) },
    );

    act(() => view.result.current.editTarget(targetID, "Updated Ada"));
    await waitFor(() => expect(callsTo(script, "read")).toHaveLength(1));
    view.unmount();
    await act(async () => {
      loaded.settle({ value: { data: { id: targetID, name: "Ada" }, etag: '"v1"' } });
    });

    expect(callsTo(script, "update")).toHaveLength(0);
  });

  it("keeps a selected target the collection no longer exposes", async () => {
    const script = signedInScript();
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }] } });
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), `${targetID}\n${otherID}`, vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(view.result.current.selections[1].available).toBe(false));
    expect(view.result.current.selections[1]).toEqual({ id: otherID, label: otherID, available: false });
    expect(view.result.current.status).toBeNull();
  });

  it("keeps a selection readable when its labels cannot be hydrated at all", async () => {
    const script = signedInScript();
    script.list.push({ fault: { kind: "unreachable" } });
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), targetID, vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(callsTo(script, "list")).toHaveLength(1));
    expect(view.result.current.selections).toEqual([{ id: targetID, label: targetID, available: true }]);
    expect(view.result.current.status).toBeNull();
  });

  it("stops hydrating at the request budget and leaves the rest addressed by id", async () => {
    const script = signedInScript();
    // One more page than the budget allows, so an unbounded hydration would
    // have something to consume and the count below would catch it.
    for (let batch = 0; batch < 5; batch += 1) script.list.push({ value: { items: [] } });
    const ids = Array.from({ length: 201 }, (_ignored, index) => bulkID(index + 1));
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), ids.join("\n"), vi.fn()), { wrapper: wrap(script) });

    await waitFor(() => expect(callsTo(script, "list")).toHaveLength(4));
    await act(async () => { await new Promise((settle) => setTimeout(settle, 0)); });
    expect(callsTo(script, "list")).toHaveLength(4);
    expect(view.result.current.selections).toHaveLength(201);
    expect(view.result.current.selections[200]).toEqual({ id: ids[200], label: ids[200], available: true });
  });

  it("chooses the keyboard-active result and preserves the ordered selection", async () => {
    const field = relationField("tags", "multiple");
    const { result, script, onChange } = openPicker(users, field, targetID, { items: [{ id: targetID, name: "Ada" }] });
    await waitFor(() => expect(result.current.selections[0].label).toBe("Ada"));

    script.list.push({ value: { items: [{ id: otherID, name: "Grace" }, { id: thirdID, name: "Hopper" }] } });
    act(() => result.current.search());
    await waitFor(() => expect(result.current.results).toHaveLength(2));

    act(() => result.current.moveActive("next"));
    act(() => result.current.moveActive("last"));
    expect(result.current.activeIndex).toBe(1);
    act(() => result.current.chooseActive());

    expect(onChange).toHaveBeenCalledWith(`${targetID}\n${thirdID}`);
    expect(result.current.status).toBe("Selected Hopper.");
  });

  it("labels a new selection from the result row it was chosen from", async () => {
    const script = signedInScript();
    script.list.push({ value: { items: [{ id: targetID, name: "Ada" }] } });
    const view = renderHook(
      ({ draft }: { draft: string }) => useRelationPicker(users, relationField("tags", "multiple"), draft, () => undefined),
      { initialProps: { draft: "" }, wrapper: wrap(script) },
    );

    act(() => view.result.current.search());
    await waitFor(() => expect(view.result.current.results).toHaveLength(1));
    act(() => view.result.current.select(targetID));
    view.rerender({ draft: targetID });

    await waitFor(() => expect(view.result.current.selections).toEqual([{ id: targetID, label: "Ada", available: true }]));
    await act(async () => { await new Promise((settle) => setTimeout(settle, 0)); });
    // The chosen row already carried the label, so no hydration follows it.
    expect(callsTo(script, "list")).toHaveLength(1);
  });

  it("removes one selection and keeps the order of the rest", async () => {
    const script = signedInScript();
    script.list.push({ value: { items: [] } });
    const onChange = vi.fn<(value: string) => void>();
    const draft = `${targetID}\n${otherID}\n${thirdID}`;
    const view = renderHook(() => useRelationPicker(users, relationField("tags", "multiple"), draft, onChange), { wrapper: wrap(script) });

    await waitFor(() => expect(view.result.current.selections).toHaveLength(3));
    act(() => view.result.current.deselect(otherID));
    expect(onChange).toHaveBeenCalledWith(`${targetID}\n${thirdID}`);
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
    const { result, onChange } = openPicker(users, field, targetID, { items: [{ id: targetID, name: "Ada" }] });
    await waitFor(() => expect(result.current.selections[0].label).toBe("Ada"));

    act(() => result.current.select(targetID));
    expect(onChange).not.toHaveBeenCalled();
    expect(result.current.status).toBe("That target is already selected.");
  });
});
