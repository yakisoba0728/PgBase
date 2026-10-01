import { describe, expect, it } from "vitest";
import { CollectionCatalog, type SchemaDocument } from "../backend/ports";
import { collection, textField } from "../test/harness";
import { authImportDocument, initial, loadedState } from "./collection-designer-state";

describe("collection identity inheritance", () => {
  it("preserves a duplicated auth policy and lets the duplicate use UUID identity", () => {
    const recordId = { kind: "text" as const, minLength: 1, maxLength: 32, pattern: "^[A-Za-z0-9]+$" };
    const source = { ...collection("members", "members-id", [textField("email", 0)]), type: "auth" as const, recordId };
    const document: SchemaDocument = {
      format: "pgbase.schema/v1",
      collections: [{ name: "members", type: "auth", recordId, fields: [{ name: "email", type: "text", required: false, unique: true, indexed: false }], userAccess: false, rules: {} }],
      authCollections: [{ name: "members", recordId, registrationEnabled: false, fields: [], rules: {}, policy: {
        passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 1024, passwordPattern: "", identityFields: ["email"],
        mfaEnabled: false, mfaDurationSeconds: 1800, mfaRule: "", otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5,
      } }],
    };
    const blank = initial({ kind: "duplicate", collection: "members" }, "members-copy");
    const duplicate = { ...blank, ...loadedState(blank, new CollectionCatalog(4, [source]), undefined, document) };

    const textDocument = JSON.parse(JSON.stringify(authImportDocument(duplicate)));
    expect(textDocument.collections[0].recordId).toEqual(recordId);
    expect(textDocument.authCollections[0].recordId).toEqual(recordId);
    const uuidDocument = JSON.parse(JSON.stringify(authImportDocument({ ...duplicate, recordId: undefined })));
    expect(uuidDocument.collections[0]).not.toHaveProperty("recordId");
    expect(uuidDocument.authCollections[0]).not.toHaveProperty("recordId");
  });
});
