import { describe, expect, it } from "vitest";
import type { Collection, CollectionField } from "../api/collections";
import { buildEndpoints, requestBody, responseRecord } from "./endpoints";
import { codeSample, dartValue, shellQuote } from "./samples";

const field = (name: string, type: CollectionField["type"], extra: Partial<CollectionField> = {}): CollectionField => ({
  id: name, name, columnName: name, type, required: false, unique: false, indexed: false, ordinal: 0, hidden: false, presentable: false, ...extra,
});

const notes: Collection = {
  id: "c1",
  name: "notes",
  type: "base",
  tableName: "notes",
  createdAt: "",
  fields: [field("title", "text"), field("count", "integer", { options: { min: "3" } }), field("secret", "text", { hidden: true }), field("photo", "file"), field("stamp", "autodate")],
};

describe("api reference", () => {
  it("builds bodies and records from the schema", () => {
    expect(requestBody(notes)).toEqual({ title: "example", count: 3, secret: "example" });
    const record = responseRecord(notes);
    expect(Object.keys(record)).toEqual(["id", "title", "count", "stamp", "created_at", "updated_at"]);
  });

  it("keeps bigint and numeric bounds exact, as JSON strings", () => {
    const big: Collection = { ...notes, fields: [field("n", "bigint", { options: { min: "-9223372036854775808" } }), field("d", "numeric")] };
    expect(requestBody(big)).toEqual({ n: "-9223372036854775808", d: "1.5" });
  });

  it("lists routes by collection type", () => {
    expect(buildEndpoints(notes, "guest").map((endpoint) => endpoint.id)).toEqual(["list", "view", "create", "update", "delete", "batch", "files-photo", "realtime"]);
    expect(buildEndpoints({ ...notes, type: "view" }, "user").map((endpoint) => endpoint.id)).toEqual(["list", "view", "realtime"]);
    expect(buildEndpoints({ ...notes, type: "auth" }, "guest", true).some((endpoint) => endpoint.path === "/api/v1/auth/notes/login")).toBe(true);
    expect(buildEndpoints({ ...notes, type: "auth" }, "guest").some((endpoint) => endpoint.path.startsWith("/api/v1/auth/"))).toBe(false);
  });

  it("writes a runnable curl list for guests", () => {
    const list = buildEndpoints(notes, "guest")[0];
    expect(codeSample("curl", list, notes, "http://localhost:8090", "guest")).toBe(
      [
        "curl --get 'http://localhost:8090/api/v1/public/collections/notes/records'",
        `--data-urlencode "filter=title ~ 'example'"`,
        "--data-urlencode 'sort=-created_at'",
        "--data-urlencode 'limit=20'",
      ].join(" \\\n  "),
    );
  });

  it("quotes shell and Dart literals", () => {
    expect(shellQuote(`a'b "$x"`)).toBe(`"a'b \\"\\$x\\""`);
    expect(dartValue({ n: 1, s: "it's", l: [true] })).toBe(`JsonObject({'n': JsonNumber('1'), 's': const JsonString('it\\'s'), 'l': JsonArray([const JsonBool(true)])})`);
  });
});
