import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useCollectionDesigner, type CollectionDesigner, type DesignerMode } from "./collection-designer";
import type { AdminRoute } from "../routing/admin-location";
import type { CollectionDefinition, FieldDefinition } from "../backend/ports";
import { callsTo, deferredStep, type BackendScript } from "../test/backend-script";
import { collection, signedInScript, sessionWrapper, textField } from "../test/harness";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";

function relationField(name: string, ordinal: number, targetCollectionId: string, patch: Partial<FieldDefinition["relation"]> = {}): FieldDefinition {
  const relation = { targetCollectionId, cardinality: "single" as const, minSelect: 0, maxSelect: 1, onDelete: "restrict" as const, ...patch };
  return { name, type: "relation", required: relation.minSelect > 0, unique: false, indexed: true, ordinal, relation };
}

const posts = collection("posts", "posts-id", [textField("title", 0, { required: true })]);
const users = collection("users", "users-id");

function open(mode: DesignerMode, collections: CollectionDefinition[] = [posts, users], version = 7) {
  const script = signedInScript();
  const navigate = vi.fn<(route: AdminRoute) => void>();
  script.catalog.push({ value: { version, collections } });
  const wrapper = sessionWrapper(memoryBackend(script), memoryHistory());
  const view = renderHook(({ current }: { current: DesignerMode }) => useCollectionDesigner(current, navigate), {
    initialProps: { current: mode },
    wrapper,
  });
  return { script, navigate, ...view };
}

async function ready(result: { current: CollectionDesigner }): Promise<void> {
  await waitFor(() => expect(result.current.phase).toBe("ready"));
}

function plan(script: BackendScript, checksum = "approved", sql = ["ALTER TABLE posts"], fields: { name: string; type: "text" }[] = [{ name: "title", type: "text" }]) {
  script.plan.push({
    value: {
      baseVersion: 7,
      checksum,
      sql,
      fields: fields.map((field) => ({ ...field, required: false, unique: false, indexed: false })),
    },
  });
}

describe("CollectionDesigner drafting", () => {
  it("keeps a relation target the administrator chose when the collection name changes", async () => {
    const { result } = open({ kind: "create" });

    act(() => result.current.editField(0, { set: "type", value: "relation" }));
    await waitFor(() => expect(result.current.relationTargets.length).toBeGreaterThan(0));
    act(() => result.current.editField(0, { set: "target", value: "users" }));
    act(() => result.current.rename("users"));

    expect(result.current.fields[0].relation?.targetCollection).toBe("users");
    act(() => result.current.rename("users_profiles"));
    expect(result.current.fields[0].relation?.targetCollection).toBe("users");
  });

  it("retargets a self relation when the collection name changes", async () => {
    const { result } = open({ kind: "create" });

    act(() => result.current.rename("articles"));
    act(() => result.current.editField(0, { set: "type", value: "relation" }));
    await waitFor(() => expect(result.current.relationTargets.some((target) => target.label === "articles (self)")).toBe(true));
    act(() => result.current.editField(0, { set: "target", value: "articles" }));
    act(() => result.current.rename("stories"));

    expect(result.current.fields[0].relation?.targetCollection).toBe("stories");
  });

  it("keeps relation bounds, required and unique consistent", async () => {
    const { result } = open({ kind: "create" });

    act(() => result.current.editField(0, { set: "unique", value: true }));
    act(() => result.current.editField(0, { set: "type", value: "relation" }));
    act(() => result.current.editField(0, { set: "cardinality", value: "multiple" }));

    expect(result.current.fields[0]).toMatchObject({ indexed: true, unique: false, hasDefault: false });
    expect(result.current.fields[0].relation).toMatchObject({ cardinality: "multiple", minSelect: "0", maxSelect: "20" });

    act(() => result.current.editField(0, { set: "minSelect", value: "2" }));
    expect(result.current.fields[0].required).toBe(true);

    act(() => result.current.editField(0, { set: "onDelete", value: "set_null" }));
    expect(result.current.fields[0]).toMatchObject({ required: false });
    expect(result.current.fields[0].relation).toMatchObject({ minSelect: "0" });
  });

  it("loads a persisted relation as a named target and round-trips it", async () => {
    const owner = relationField("owner", 1, "users-id", { cardinality: "multiple", minSelect: 2, maxSelect: 20, onDelete: "cascade" });
    const withRelation = collection("posts", "posts-id", [textField("title", 0), owner]);
    const { result, script } = open({ kind: "edit", collection: "posts" }, [withRelation, users]);
    await ready(result);

    expect(result.current.fields[1].relation).toEqual({ targetCollection: "users", cardinality: "multiple", minSelect: "2", maxSelect: "20", onDelete: "cascade" });

    plan(script);
    act(() => result.current.requestPreview());
    await waitFor(() => expect(callsTo(script, "plan")).toHaveLength(1));
    expect(callsTo(script, "plan")[0].detail.change).toMatchObject({
      operation: "update",
      name: "posts",
      fields: [
        { name: "title", type: "text" },
        { name: "owner", type: "relation", required: true, relation: { targetCollection: "users", cardinality: "multiple", minSelect: 2, maxSelect: 20, onDelete: "cascade" } },
      ],
    });
  });

  it("explains a staged required relation migration only for a field the collection does not have yet", async () => {
    const { result } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    expect(result.current.isNewField("title")).toBe(false);
    act(() => result.current.addField());
    act(() => result.current.editField(1, { set: "name", value: "owner" }));
    expect(result.current.isNewField("owner")).toBe(true);
  });
});

describe("CollectionDesigner previewing", () => {
  it("invalidates the preview as soon as the draft changes", async () => {
    const { result, script } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    plan(script);
    act(() => result.current.requestPreview());
    await waitFor(() => expect(result.current.preview).not.toBeNull());

    act(() => result.current.editField(0, { set: "name", value: "headline" }));
    expect(result.current.preview).toBeNull();
  });

  it("requires confirmation before a change that drops field data", async () => {
    const { result, script } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    plan(script, "approved", ["ALTER TABLE posts"], []);
    act(() => result.current.removeField(0));
    act(() => result.current.requestPreview());
    await waitFor(() => expect(result.current.preview).not.toBeNull());

    expect(result.current.removedFields).toEqual(["title"]);
    expect(result.current.applyBlocked).toBe(true);
    act(() => result.current.requestApply());
    expect(callsTo(script, "apply")).toHaveLength(0);

    script.apply.push({ value: undefined });
    act(() => result.current.confirmDestructive(true));
    expect(result.current.applyBlocked).toBe(false);
    act(() => result.current.requestApply());
    await waitFor(() => expect(callsTo(script, "apply")).toHaveLength(1));
  });

  it.each([
    ["create" as const, "same-version-conflict" as const, "A collection with this name likely already exists."],
    ["create" as const, "catalog-moved" as const, "The catalog changed. Preview the collection again before applying."],
    ["edit" as const, "same-version-conflict" as const, "The plan conflicts with the current state. Preview again; if it keeps failing, existing data may be blocking it."],
    ["edit" as const, "catalog-moved" as const, "The catalog changed. Preview the collection again before applying."],
  ])("names a %s %s conflict and clears the stale preview", async (kind, reason, message) => {
    const mode: DesignerMode = kind === "create" ? { kind: "create" } : { kind: "edit", collection: "posts" };
    const { result, script } = open(mode);
    if (kind === "edit") await ready(result);
    else act(() => result.current.rename("posts"));

    plan(script);
    script.apply.push({ fault: { kind: reason } });
    act(() => result.current.requestPreview());
    await waitFor(() => expect(result.current.preview).not.toBeNull());
    act(() => result.current.requestApply());

    await waitFor(() => expect(result.current.error).toBe(message));
    expect(result.current.preview).toBeNull();
  });

  it("explains a change existing data blocks with the offending detail codes", async () => {
    const { result, script } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    plan(script);
    script.apply.push({ fault: { kind: "data-blocked", details: { title: ["not_null_violation"] } } });
    act(() => result.current.requestPreview());
    await waitFor(() => expect(result.current.preview).not.toBeNull());
    act(() => result.current.requestApply());

    await waitFor(() => expect(result.current.error).toBe(
      "Existing data blocks this change (for example missing or duplicate values). title: not_null_violation",
    ));
  });

  it("reports an unknown collection as missing", async () => {
    const { result } = open({ kind: "edit", collection: "ghosts" });
    await waitFor(() => expect(result.current.phase).toBe("missing"));
  });

  it.each([
    ["server", () => ({ fault: { kind: "unreachable" as const } })],
    ["transport", () => ({ pending: Promise.reject(new TypeError("Failed to fetch")) as never })],
  ])("tells a %s catalog failure apart from a missing collection", async (_kind, step) => {
    const script = signedInScript();
    script.catalog.push(step());
    const { result } = renderHook(() => useCollectionDesigner({ kind: "edit", collection: "posts" }, vi.fn()), {
      wrapper: sessionWrapper(memoryBackend(script), memoryHistory()),
    });

    await waitFor(() => expect(result.current.phase).toBe("unavailable"));
  });
});

describe("CollectionDesigner deletion", () => {
  it("requires the deletion plan before dropping a collection", async () => {
    const { result, script, navigate } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    expect(result.current.deleteTarget).toBeNull();
    act(() => result.current.requestDelete());
    expect(callsTo(script, "apply")).toHaveLength(0);

    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    script.apply.push({ value: undefined });
    act(() => result.current.requestDeletePlan());
    await waitFor(() => expect(result.current.deleteTarget?.sql).toEqual(["DROP TABLE posts"]));

    act(() => result.current.confirmDeleteName("posts"));
    act(() => result.current.requestDelete());
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ kind: "collections" }));
  });

  it("pins the identity it shows and refuses every name but that one", async () => {
    const { result, script } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    script.apply.push({ value: undefined });
    act(() => result.current.requestDeletePlan());
    await waitFor(() => expect(result.current.deleteTarget).not.toBeNull());
    expect(result.current.deleteTarget).toEqual({ collection: "posts", tableName: "posts", baseVersion: 7, sql: ["DROP TABLE posts"] });

    for (const typed of ["", "post", "POSTS", " posts"]) {
      act(() => result.current.confirmDeleteName(typed));
      expect(result.current.deleteConfirmed).toBe(false);
      act(() => result.current.requestDelete());
    }
    expect(callsTo(script, "apply")).toHaveLength(0);

    act(() => result.current.confirmDeleteName("posts"));
    expect(result.current.deleteConfirmed).toBe(true);
    act(() => result.current.requestDelete());
    await waitFor(() => expect(callsTo(script, "apply")).toHaveLength(1));
    expect(callsTo(script, "apply")[0].detail.request).toEqual({ operation: "delete", name: "posts", checksum: "drop", baseVersion: 7 });
  });

  it("keeps the pinned target when the draft is renamed under the confirmation", async () => {
    const { result, script } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    act(() => result.current.requestDeletePlan());
    await waitFor(() => expect(result.current.deleteTarget).not.toBeNull());

    act(() => result.current.rename("ghosts"));
    expect(result.current.deleteTarget?.collection).toBe("posts");
    act(() => result.current.confirmDeleteName("ghosts"));
    act(() => result.current.requestDelete());
    expect(callsTo(script, "apply")).toHaveLength(0);
  });

  it("drops the confirmation with the plan the catalog moved under", async () => {
    const { result, script } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    script.apply.push({ fault: { kind: "catalog-moved" } });
    act(() => result.current.requestDeletePlan());
    await waitFor(() => expect(result.current.deleteTarget).not.toBeNull());
    act(() => result.current.confirmDeleteName("posts"));
    act(() => result.current.requestDelete());

    await waitFor(() => expect(result.current.deleteError).toBe("The catalog changed. Preview the deletion again before applying."));
    expect(result.current.deleteTarget).toBeNull();
    expect(result.current.deleteConfirmText).toBe("");
    act(() => result.current.requestDelete());
    expect(callsTo(script, "apply")).toHaveLength(1);
  });

  it("explains a collection another collection still references", async () => {
    const { result, script } = open({ kind: "edit", collection: "posts" });
    await ready(result);

    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    script.apply.push({ fault: { kind: "referenced", details: { name: ["collection_referenced"] } } });
    act(() => result.current.requestDeletePlan());
    await waitFor(() => expect(result.current.deleteTarget).not.toBeNull());
    act(() => result.current.confirmDeleteName("posts"));
    act(() => result.current.requestDelete());

    await waitFor(() => expect(result.current.deleteError).toBe("Collection is referenced. name: collection_referenced"));
    expect(result.current.deleteTarget).toBeNull();
  });
});

describe("CollectionDesigner resource isolation", () => {
  it("never carries one collection's delete plan into another in the same instance", async () => {
    const { result, script, rerender } = open({ kind: "edit", collection: "posts" }, [posts, users]);
    await ready(result);

    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    act(() => result.current.requestDeletePlan());
    await waitFor(() => expect(result.current.deleteTarget).not.toBeNull());

    script.catalog.push({ value: { version: 7, collections: [posts, users] } });
    rerender({ current: { kind: "edit", collection: "users" } });

    expect(result.current.deleteTarget).toBeNull();
    expect(result.current.fields).toEqual([]);
    await waitFor(() => expect(result.current.name).toBe("users"));
    expect(result.current.deleteTarget).toBeNull();
  });

  it("drops a delete result that arrives after the designer moved to another collection", async () => {
    const { result, script, rerender, navigate } = open({ kind: "edit", collection: "posts" }, [posts, users]);
    await ready(result);

    script.plan.push({ value: { baseVersion: 7, checksum: "drop", fields: [], sql: ["DROP TABLE posts"] } });
    act(() => result.current.requestDeletePlan());
    await waitFor(() => expect(result.current.deleteTarget).not.toBeNull());

    const deletion = deferredStep<void>();
    script.apply.push(deletion.step);
    act(() => result.current.confirmDeleteName("posts"));
    act(() => result.current.requestDelete());

    script.catalog.push({ value: { version: 7, collections: [posts, users] } });
    rerender({ current: { kind: "edit", collection: "users" } });
    await waitFor(() => expect(result.current.name).toBe("users"));

    await act(async () => { deletion.settle({ value: undefined }); });

    expect(navigate).not.toHaveBeenCalled();
    expect(result.current.deleteTarget).toBeNull();
    expect(result.current.deleteError).toBeNull();
  });

  it("drops a create result that arrives after the route moved to an edit page", async () => {
    const { result, script, rerender, navigate } = open({ kind: "create" }, [posts, users]);

    plan(script, "created", ["CREATE TABLE articles"]);
    const creation = deferredStep<void>();
    script.apply.push(creation.step);
    act(() => result.current.rename("articles"));
    act(() => result.current.editField(0, { set: "name", value: "title" }));
    act(() => result.current.requestPreview());
    await waitFor(() => expect(result.current.preview).not.toBeNull());

    act(() => result.current.requestApply());
    script.catalog.push({ value: { version: 7, collections: [posts, users] } });
    rerender({ current: { kind: "edit", collection: "posts" } });
    await waitFor(() => expect(result.current.name).toBe("posts"));

    await act(async () => { creation.settle({ value: undefined }); });

    expect(navigate).not.toHaveBeenCalled();
    expect(result.current.fields.map((field) => field.name)).toEqual(["title"]);
  });
});
