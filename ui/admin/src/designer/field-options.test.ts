import { describe, expect, it } from "vitest";
import { buildInput } from "./collection-designer";
import { draftFieldOptions, fieldOptionsInput } from "./field-options";

describe("field options", () => {
  it("preserves precision, choice lines and zero bounds across editing", () => {
    const options = { min: "9007199254740993", max: "9999999999999999", minLength: 0, allowedValues: [" a ", "b", "", "line\nbreak"] };
    expect(fieldOptionsInput(draftFieldOptions(options))).toEqual(options);
  });
  it("sends identity with a rename and explicit empty options when clearing validators", () => {
    const change = buildInput("update", "posts", [{ id: "field-id", name: "renamed", type: "text", required: false, unique: false, indexed: false, hasDefault: false, defaultText: "" }]);
    expect(change.fields?.[0]).toMatchObject({ id: "field-id", name: "renamed", options: {} });
  });
  it("rejects invalid count bounds without silently rounding", () => {
    expect(() => fieldOptionsInput({ minSelect: "1.5" })).toThrow();
    expect(() => fieldOptionsInput({ minLength: "5", maxLength: "4" })).toThrow();
  });
  it("round trips file limits, MIME wildcards, and thumbnail modes", () => {
    const options = { maxFileSize: 5242880, maxSelect: 4, mimeTypes: ["image/*", "application/pdf"], thumbs: ["100x100", "100x50t", "100x50b", "100x50f"] };
    expect(fieldOptionsInput(draftFieldOptions(options))).toEqual(options);
  });
  it("round trips generation and both canonical domain lists", () => {
    const options = { autogeneratePattern: "user-[a-z]{8}@example[.]com", format: "email" as const, allowedDomains: ["example.com"], deniedDomains: ["private.example.com"] };
    expect(fieldOptionsInput(draftFieldOptions(options))).toEqual(options);
  });
});
