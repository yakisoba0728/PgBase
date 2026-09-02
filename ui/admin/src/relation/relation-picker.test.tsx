import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CollectionDefinition, FieldDefinition } from "../backend/ports";
import { callsTo, type BackendScript } from "../test/backend-script";
import { collection, sessionWrapper, signedInScript, textField } from "../test/harness";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";
import { useRelationPicker } from "./relation-picker";

const targetID = "11111111-1111-4111-8111-111111111111";

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
