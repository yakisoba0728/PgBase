import { LosslessNumber } from "lossless-json";
import { describe, expect, it, vi } from "vitest";
import { CollectionCatalog, CreateAttempt, PlannedChange, VersionedRecord, type CollectionChange, type CollectionDefinition } from "./ports";

function collection(name: string, id: string): CollectionDefinition {
  return { id, name, tableName: name, fields: [] };
}

describe("CollectionCatalog", () => {
  it("never resolves an inherited property as a collection", () => {
    const own = collection("constructor", "own-id");
    const inherited = collection("shadow", "inherited-id");
    const raw = Object.create({ inherited, shadow: inherited }) as Record<string, CollectionDefinition>;
    Object.defineProperty(raw, "constructor", { configurable: true, enumerable: true, value: own });
    const catalog = new CollectionCatalog(1, Object.values(raw));

    expect(catalog.get("shadow")).toBeUndefined();
    expect(catalog.get("constructor")).toBe(own);
    expect(catalog.byID("inherited-id")).toBeUndefined();
    expect(catalog.byID("own-id")).toBe(own);
  });

  it("lists collections in name order", () => {
    const catalog = new CollectionCatalog(3, [collection("users", "u"), collection("articles", "a"), collection("posts", "p")]);
    expect(catalog.all().map((entry) => entry.name)).toEqual(["articles", "posts", "users"]);
    expect(catalog.version).toBe(3);
  });
});

describe("PlannedChange", () => {
  it("applies only under the operation, name, checksum and base version it was issued for", () => {
    const change: CollectionChange = { operation: "update", name: "posts", fields: [] };
    const plan = new PlannedChange(change, "checksum-a", 7, [], ["ALTER TABLE posts"]);

    expect(plan.collection).toBe("posts");
    expect(plan.operation).toBe("update");
    expect(plan.applyRequest()).toEqual({ operation: "update", name: "posts", fields: [], checksum: "checksum-a", baseVersion: 7 });
  });

  it("recognises only the exact input it previewed", () => {
    const change: CollectionChange = { operation: "create", name: "posts", fields: [{ name: "title", type: "text", required: false, unique: false, indexed: true }] };
    const plan = new PlannedChange(change, "checksum-a", 7, [], []);

    expect(plan.matches(change)).toBe(true);
    expect(plan.matches({ ...change, name: "other" })).toBe(false);
    expect(plan.matches({ ...change, fields: [] })).toBe(false);
  });
});

describe("VersionedRecord", () => {
  it("keeps one record's precondition with that record's own identity", () => {
    const first = new VersionedRecord("posts", "one", { id: "one" }, '"pgb1.one"');
    const second = new VersionedRecord("users", "two", { id: "two" }, '"pgb1.two"');

    expect(first.precondition()).toBe('"pgb1.one"');
    expect(second.precondition()).toBe('"pgb1.two"');
    expect(first.collection).toBe("posts");
    expect(second.collection).toBe("users");
  });
});

describe("CreateAttempt", () => {
  it("reuses one idempotency key per unchanged create body and rotates it otherwise", () => {
    const generate = vi.fn()
      .mockReturnValueOnce("11111111-1111-4111-8111-111111111111")
      .mockReturnValueOnce("22222222-2222-4222-8222-222222222222")
      .mockReturnValueOnce("33333333-3333-4333-8333-333333333333");
    const attempt = new CreateAttempt("posts", generate);
    const initial = { sequence: new LosslessNumber("9007199254740993") };

    expect(attempt.keyFor(initial)).toBe("11111111-1111-4111-8111-111111111111");
    expect(attempt.keyFor(initial)).toBe("11111111-1111-4111-8111-111111111111");
    expect(attempt.keyFor({ sequence: new LosslessNumber("9007199254740994") })).toBe("22222222-2222-4222-8222-222222222222");
    attempt.reset();
    expect(attempt.keyFor({ sequence: new LosslessNumber("9007199254740994") })).toBe("33333333-3333-4333-8333-333333333333");
    expect(generate).toHaveBeenCalledTimes(3);
    expect(attempt.collection).toBe("posts");
  });
});
