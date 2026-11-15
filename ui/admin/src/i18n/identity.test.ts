import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

it.each(["en", "ko"])("describes schema capabilities independently in %s", (language) => {
  const source = readFileSync(new URL(`./${language}.ts`, import.meta.url), "utf8");
  for (const key of ["schema.compatibilityName", "schema.compatibilityHint", "schema.mode.pocketbase", "schema.recordId.text"]) {
    const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const match = source.match(new RegExp(`"${escaped}"\\s*:\\s*"([^"]*)"`));
    expect(match, key).not.toBeNull();
    expect(match?.[1], key).not.toMatch(/pocketbase/i);
  }
  // A legacy wire value is not silently presented as the native strict mode.
  expect(source).toContain(language === "en" ? '"schema.mode.pocketbase": "Legacy compatibility"' : '"schema.mode.pocketbase": "레거시 호환"');
});
