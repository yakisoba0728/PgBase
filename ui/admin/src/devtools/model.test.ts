import { describe, expect, it } from "vitest";
import type { Collection } from "../api/collections";
import type { SchemaDocument, SchemaImportPreview } from "../api/schema";
import { ApiError } from "../api/http";
import { errorLocation, importSections, mayHaveRun, pickCollections, problemList, recallStatements, rememberStatement, statementText } from "./model";

describe("mayHaveRun", () => {
  it("treats only a refusal before the start as proof that a write did not run", () => {
    const cases: [ApiError | Error, boolean][] = [
      [new ApiError(0, "network_error", ""), true],
      [new ApiError(408, "invalid_response", ""), true],
      [new ApiError(500, "internal_error", ""), true],
      [new ApiError(502, "invalid_response", ""), true],
      [new ApiError(503, "unavailable", ""), true],
      [new ApiError(504, "invalid_response", ""), true],
      [new Error("boom"), true],
      [new ApiError(400, "invalid_request", ""), false],
      [new ApiError(403, "csrf_rejected", ""), false],
      [new ApiError(409, "conflict", ""), false],
      [new ApiError(429, "busy", ""), false],
    ];
    for (const [error, expected] of cases) expect([error instanceof ApiError ? error.status : error.message, mayHaveRun(error)]).toEqual([error instanceof ApiError ? error.status : error.message, expected]);
  });
});

const field = (name: string, id?: string) => ({ id, name, type: "text", required: false, unique: false, indexed: false, ordinal: 0, hidden: false, presentable: false });
const doc = (names: string[]): SchemaDocument => ({ format: "pgbase.schema/v1", collections: names.map((name) => ({ name, userAccess: false, rules: null })), authCollections: [] });

describe("pickCollections", () => {
  it("drops skipped collections and keeps the rest of the document", () => {
    const picked = pickCollections({ ...doc(["a", "b"]), deleteMissing: true }, ["a"]);
    expect(picked.collections.map((c) => c.name)).toEqual(["b"]);
    expect(picked.deleteMissing).toBe(true);
  });
});

describe("importSections", () => {
  const catalog = [
    { id: "c1", name: "tags", type: "base", fields: [{ ...field("label", "f1") }], indexes: [] },
    { id: "c2", name: "drafts", type: "base", fields: [{ ...field("body", "f2") }], indexes: [] },
  ] as unknown as Collection[];

  it("marks a collection missing from the document as a destructive delete and a new field as an add", () => {
    const preview = {
      baseVersion: 2,
      checksum: "x",
      policyChecksum: "y",
      document: doc(["tags"]),
      changes: [
        { baseVersion: 2, checksum: "a", definition: { name: "drafts", tableName: "drafts", fields: [field("body", "f2")] }, sql: ['DROP TABLE "app"."drafts"'] },
        { baseVersion: 3, checksum: "b", definition: { name: "tags", tableName: "tags", fields: [field("label", "f1"), field("color")] }, sql: ['ALTER TABLE "app"."tags" ADD COLUMN "color" text'] },
        { baseVersion: 4, checksum: "c", definition: { name: "tags", tableName: "tags", fields: [] }, sql: null },
      ],
    } as unknown as SchemaImportPreview;
    const [drafts, tags, ...rest] = importSections(preview, catalog);
    expect(rest).toEqual([]);
    expect(drafts).toMatchObject({ name: "drafts", danger: true, items: [{ kind: "remove", target: "collection" }] });
    expect(tags.danger).toBe(false);
    expect(tags.items).toEqual([{ kind: "add", target: "field", name: "color", detail: "text", danger: false }]);
    expect(tags.sql).toHaveLength(1);
  });
});

describe("problemList", () => {
  it("flattens validation details", () => {
    expect(problemList({ "fields.label.type": ["type_conflict"], name: "invalid_name" })).toEqual([
      { path: "fields.label.type", codes: ["type_conflict"] },
      { path: "name", codes: ["invalid_name"] },
    ]);
    expect(problemList(undefined)).toEqual([]);
  });
});

describe("SQL helpers", () => {
  it("strips only trailing semicolons and space", () => {
    expect(statementText("  select 1;; \n")).toBe("  select 1");
  });

  it("locates a 1-based position by line and column", () => {
    const sql = "select 1\nfrom nowhere\nwhere";
    expect(errorLocation(sql, 10)).toEqual({ line: 2, column: 1, text: "from nowhere" });
    expect(errorLocation(sql, 1)).toEqual({ line: 1, column: 1, text: "select 1" });
    expect(errorLocation(sql, 0)).toBeNull();
    expect(errorLocation(sql, 999)).toBeNull();
  });

  it("converts a UTF-8 byte offset past non-ASCII text", () => {
    // '한글' is 6 bytes for 2 characters: "x" is byte 17 but character 13.
    const sql = "select '한글' x y";
    expect(errorLocation(sql, 17, "byte")).toEqual({ line: 1, column: 13, text: sql });
    expect(sql[12]).toBe("x");
  });

  it("remembers statements per session scope", () => {
    rememberStatement("s1", "id1", "select 1");
    rememberStatement("s1", "id2", "select 2");
    expect(recallStatements("s1")).toEqual({ id1: "select 1", id2: "select 2" });
    expect(recallStatements("other")).toEqual({});
  });
});
