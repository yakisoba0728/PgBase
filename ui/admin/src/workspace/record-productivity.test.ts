import type { FieldDefinition } from "../backend/ports";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearAdministratorRecordStorage, clearDraft, readColumns, readDraft, writeColumns, writeDraft } from "./record-productivity";

const alice = { administrator: "alice", collection: "posts", record: "new" };

afterEach(() => localStorage.clear());

describe("record-local productivity state", () => {
  it("persists valid columns while keeping id visible and ignoring removed fields", () => {
    writeColumns(alice, ["title"]);
    expect(readColumns(alice, ["id", "title", "removed"])).toEqual(["id", "title"]);
    expect(readColumns({ ...alice, collection: "users" }, ["id", "email"])).toEqual(["id", "email"]);
  });

  it("scopes drafts to administrator, collection and record and clears only the intended scope", () => {
    writeDraft(alice, { title: { kind: "value", value: "private draft" } });
    expect(readDraft(alice)).toEqual({ title: { kind: "value", value: "private draft" } });
    expect(readDraft({ ...alice, administrator: "bob" })).toBeNull();
    clearDraft(alice);
    expect(readDraft(alice)).toBeNull();
  });

  it("clears every record preference and draft owned by the signing-out administrator", () => {
    writeDraft(alice, { title: { kind: "value", value: "draft" } });
    writeColumns(alice, ["id"]);
    writeDraft({ ...alice, administrator: "bob" }, { title: { kind: "value", value: "kept" } });
    clearAdministratorRecordStorage("alice");
    expect(readDraft(alice)).toBeNull();
    expect(readDraft({ ...alice, administrator: "bob" })).not.toBeNull();
  });
  it("does not persist password and token fields", () => {
    writeDraft(alice, { title: { kind: "value", value: "draft" }, password: { kind: "value", value: "secret" }, api_token: { kind: "value", value: "secret" } });
    expect(readDraft(alice)).toEqual({ title: { kind: "value", value: "draft" } });
  });
  it("excludes password fields by type even when their names are innocuous", () => {
    const fields: FieldDefinition[] = [{ name: "pin", type: "password", required: false, unique: false, indexed: false, ordinal: 0 }, { name: "title", type: "text", required: false, unique: false, indexed: false, ordinal: 1 }];
    const draft = { pin: { kind: "value" as const, value: "do-not-persist" }, title: { kind: "value" as const, value: "draft" } };
    writeDraft(alice, draft, fields);
    expect(readDraft(alice, fields)).toEqual({ title: draft.title });
    expect(JSON.stringify(localStorage)).not.toContain("do-not-persist");
    writeDraft(alice, draft);
    expect(readDraft(alice, fields)).toEqual({ title: draft.title });
    expect(JSON.stringify(localStorage)).not.toContain("do-not-persist");
  });
  it("keeps storage denial from interrupting logout or edits", () => {
    const set = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("denied"); });
    expect(() => writeDraft(alice, {})).not.toThrow();
    expect(() => writeColumns(alice, ["id"])).not.toThrow();
    set.mockRestore();
    const remove = vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("denied"); });
    expect(() => clearDraft(alice)).not.toThrow();
    writeColumns(alice, ["id"]);
    expect(() => clearAdministratorRecordStorage("alice")).not.toThrow();
    remove.mockRestore();
  });

});
