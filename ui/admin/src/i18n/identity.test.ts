import { expect, it } from "vitest";
import { en } from "./en";
import { ko } from "./ko";

it.each([{ language: "en", messages: en, legacy: "Legacy compatibility" }, { language: "ko", messages: ko, legacy: "레거시 호환" }])("describes schema capabilities independently in $language", ({ messages, legacy }) => {
  for (const key of ["schema.compatibilityName", "schema.compatibilityHint", "schema.mode.pocketbase", "schema.recordId.text"] as const) {
    expect(messages[key], key).not.toMatch(/pocketbase/i);
  }
  // A legacy wire value is not silently presented as the native strict mode.
  expect(messages["schema.mode.pocketbase"]).toBe(legacy);
});
