/// <reference types="node" />
import { describe, expect, it } from "vitest";
import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { collectionExample } from "./collection-api-examples";
import type { CollectionDefinition } from "../backend/ports";

const collection: CollectionDefinition = { name: "posts", tableName: "posts", fields: [
  { name: "title", type: "text", required: true, unique: false, indexed: true, ordinal: 0, options: { minLength: 2 } },
  { name: "rank", type: "bigint", required: false, unique: false, indexed: false, ordinal: 1 },
] };

describe("collection API examples", () => {
  it("typechecks generated user CRUD against the actual shipped SDK declarations", () => {
    const directory = mkdtempSync(resolve(tmpdir(), "pgbase-api-example-"));
    try {
      const file = resolve(directory, "example.ts");
      const code = collectionExample(collection, "http://localhost:8090", "user").replace('"@pgbase/sdk"', JSON.stringify(resolve("../../sdk/typescript/dist/index.js")));
      writeFileSync(file, code);
      writeFileSync(resolve(directory, "tsconfig.json"), JSON.stringify({ compilerOptions: { target: "ES2022", module: "ESNext", moduleResolution: "Bundler", strict: true, noEmit: true, skipLibCheck: true, types: [] }, files: [file] }));
      expect(() => execFileSync(process.execPath, [resolve("node_modules/typescript/bin/tsc"), "-p", resolve(directory, "tsconfig.json")], { encoding: "utf8" })).not.toThrow();
    } finally { rmSync(directory, { recursive: true, force: true }); }
  });
  it("uses public/service paths and leaves view examples read-only", () => {
    expect(collectionExample(collection, "http://localhost", "anonymous")).toContain("/api/v1/public/collections/posts/records");
    expect(collectionExample(collection, "http://localhost", "service")).toContain("Bearer YOUR_SERVICE_TOKEN");
    expect(collectionExample({ ...collection, view: { source: "original", projection: ["title"] } }, "http://localhost", "user")).not.toContain("createWithETag");
  });
});
