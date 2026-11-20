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

  it("uses the public SDK route for guests and configures bearer authentication for users", () => {
    const [guestList, , guestCreate] = buildEndpoints(notes, "guest");
    const [userList] = buildEndpoints(notes, "user");
    const origin = "https://example.test";
    expect(codeSample("ts", guestList, notes, origin, "guest")).toContain('client.send("/api/v1/public/collections/notes/records?filter=');
    expect(codeSample("dart", guestList, notes, origin, "guest")).toContain("'/api/v1/public/collections/notes/records'");
    expect(codeSample("ts", guestCreate, notes, origin, "guest")).toContain('"Idempotency-Key": "create-0000000001"');
    expect(codeSample("dart", guestCreate, notes, origin, "guest")).toContain("body: JsonObject(");
    expect(codeSample("ts", userList, notes, origin, "user")).toContain('client.auth.save("<token>")');
    expect(codeSample("dart", userList, notes, origin, "user")).toContain("await client.auth.save('<token>')");
  });

  it("computes upload headers and quotes the local file in the curl form", () => {
    const upload = buildEndpoints(notes, "user").find((endpoint) => endpoint.id === "files-photo")!;
    const sample = codeSample("curl", upload, notes, "https://example.test", "user");
    expect(sample).toContain('size=$(wc -c < "$file") &&');
    expect(sample).toContain('sha256=$(openssl dgst -sha256 -r < "$file") &&');
    expect(sample).toContain('-H "X-PgBase-File-Size: $((size))"');
    expect(sample).toContain('-H "X-PgBase-File-SHA256: ${sha256%% *}"');
    expect(sample).toContain('--form "file=@\\\"${escaped_file%.}\\\""');
    expect(sample).toContain('Authorization: Bearer $PGBASE_TOKEN');
    expect(sample).not.toContain('<sha256 hex>');
  });

  it("documents CRUD and batch for service tokens, and only reads for views", () => {
    const endpoints = buildEndpoints({ ...notes, type: "auth" }, "service", true);
    expect(endpoints.map((endpoint) => endpoint.id)).toEqual(["list", "view", "create", "update", "delete", "batch"]);
    expect(buildEndpoints({ ...notes, type: "view" }, "service").map((endpoint) => endpoint.id)).toEqual(["list", "view"]);
    for (const endpoint of endpoints) {
      expect(endpoint.path).toMatch(/^\/api\/v1\/service\/collections\//);
      expect(codeSample("curl", endpoint, notes, "https://example.test", "service")).toContain('Authorization: Bearer $PGBASE_SERVICE_TOKEN');
    }
    expect(codeSample("curl", endpoints[0], notes, "https://example.test", "service")).not.toContain("filter=");
    expect(endpoints[5].path).toBe("/api/v1/service/collections/batch");
  });

  it("quotes shell and Dart literals", () => {
    expect(shellQuote(`a'b "$x"`)).toBe(`"a'b \\"\\$x\\""`);
    expect(dartValue({ n: 1, s: "it's", l: [true] })).toBe(`JsonObject({'n': JsonNumber('1'), 's': const JsonString('it\\'s'), 'l': JsonArray([const JsonBool(true)])})`);
  });
});

it("generates bounded values, authenticated batches and required registration profiles", () => {
  const collection = { ...notes, type: "auth" as const, fields: [field("i", "integer", { options: { max: "0" } }), field("n", "numeric", { options: { max: "-1.0001" } }), field("nick", "text", { required: true, options: { allowedValues: ["line\nbreak"] } })] };
  expect(requestBody(collection)).toMatchObject({ i: 0, n: "-1.0001" });
  const endpoints = buildEndpoints(collection, "user", true);
  const batch = endpoints.find((endpoint) => endpoint.id === "batch")!;
  expect(codeSample("curl", batch, collection, "https://example.test", "user")).toContain("Authorization: Bearer");
  const register = endpoints.find((endpoint) => endpoint.id === "authRegister")!;
  expect(register.body?.fields).toMatchObject({ nick: "line\nbreak" });
  expect(codeSample("ts", register, collection, "https://example.test", "user")).toContain("profile:");
  expect(codeSample("dart", register, collection, "https://example.test", "user")).toContain("profile:");
  expect(dartValue("line\nbreak")).toContain("line\\nbreak");
});

it("sends unsafe bounded integers as exact JSON text in JS examples and exact Dart numbers", () => {
  const collection = { ...notes, type: "auth" as const, fields: [field("n", "bigint", { options: { min: "9007199254740993" } })] };
  const register = buildEndpoints(collection, "user", true).find((endpoint) => endpoint.id === "authRegister")!;
  const ts = codeSample("ts", register, collection, "https://example.test", "user");
  expect(ts).toContain("await fetch(");
  expect(ts).toContain("9007199254740993");
  expect(ts).not.toContain("9007199254740992");
  expect(codeSample("dart", register, collection, "https://example.test", "user")).toContain("JsonNumber('9007199254740993')");
});
