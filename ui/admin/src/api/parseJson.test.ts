import { describe, expect, it } from "vitest";
import { mayHoldUnsafeNumber, parseJson, parseJsonLossless } from "./http";

/** parseJson must give exactly what the lossless parser gives: the same value, or the same error. */
function expectSameAsLossless(text: string) {
  let expected: { value: unknown } | { error: string };
  try {
    expected = { value: parseJsonLossless(text) };
  } catch (error) {
    expected = { error: String(error) };
  }
  if ("error" in expected) {
    expect(() => parseJson(text), text).toThrow(expected.error.replace(/^SyntaxError: /, ""));
  } else {
    expect(parseJson(text), text).toStrictEqual(expected.value);
  }
}

describe("parseJson fast path", () => {
  it.each([
    ["999999999999999", false],
    ["-999999999999999", false],
    ["9999999999999999", true],
    ["9007199254740993", true],
    ["123456789.012345", false],
    ["1234567890.123456", true],
    ["0.30000000000000004", true],
    ["1e2", true],
    ["1E+2", true],
    ["-1.5e-3", true],
    ["[1, 2 ,\n3e1]", true],
    ['{"a": 12.5, "b": [0, -0, -0.0]}', false],
    ['{"id":"8f3e41a2"}', false],
    ['{"note":"x,1e5"}', true],
  ])("decides %s → lossless: %s", (text, lossless) => {
    expect(mayHoldUnsafeNumber(text)).toBe(lossless);
    expectSameAsLossless(text);
  });

  it.each([
    "-0",
    "-0.0",
    "0.000000000000001",
    "0.0000001",
    "1.10",
    "100000000000000.0",
    "9007199254740992.0",
    "4.9e-324",
    "1e400",
    "1e-400",
    " \n\t42\r ",
    '"plain"',
    '{"a":1,"a":2}',
    '{"a":{"b":1},"a":{"b":1}}',
    '{"__proto__":{"x":1},"y":2}',
    '{"text":"{\\"inner\\":1}","n":3}',
    '{"":1,"a\\"":2}',
    '"\\ud800 lone"',
    "",
    "01",
    "{",
    "[1,]",
    '"\\x"',
    '"a\u0001b"',
    "﻿{}",
    "NaN",
    "[.5]",
    "[1.]",
    "true false",
  ])("matches the lossless parser on %j", (text) => expectSameAsLossless(text));

  it("matches the lossless parser on random documents", () => {
    let seed = 0x2f6e2b1;
    const random = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
    const pick = <T,>(items: readonly T[]) => items[Math.floor(random() * items.length)]!;
    const digits = (count: number) => Array.from({ length: count }, () => Math.floor(random() * 10)).join("");
    const number = () => {
      const int = pick(["0", `${1 + Math.floor(random() * 9)}${digits(Math.floor(random() * 19))}`]);
      const frac = random() < 0.4 ? `.${digits(1 + Math.floor(random() * 18))}` : "";
      const exp = random() < 0.15 ? `${pick(["e", "E"])}${pick(["", "+", "-"])}${1 + Math.floor(random() * 330)}` : "";
      return `${random() < 0.3 ? "-" : ""}${int}${frac}${exp}`;
    };
    const string = () => JSON.stringify(Array.from({ length: Math.floor(random() * 6) }, () => pick(["a", "1", "e", ":", ",", '"', "\\", "[", "é", "\n", " ", "3e4", " "])).join(""));
    const space = () => pick(["", "", " ", "\n  "]);
    const value = (depth: number): string => {
      const roll = random();
      if (depth > 3 || roll < 0.45) return pick([number, number, string, () => pick(["true", "false", "null"])])();
      if (roll < 0.7) return `[${space()}${Array.from({ length: Math.floor(random() * 5) }, () => value(depth + 1)).join(`${space()},${space()}`)}]`;
      const keys = ["id", "name", "n", "a", "__proto__", "x:y"];
      const entries = Array.from({ length: Math.floor(random() * 5) }, () => `${JSON.stringify(pick(keys))}${space()}:${space()}${value(depth + 1)}`);
      return `{${space()}${entries.join(",")}${space()}}`;
    };
    let fast = 0;
    for (let run = 0; run < 3000; run++) {
      const text = `${space()}${value(0)}${space()}`;
      if (!mayHoldUnsafeNumber(text)) fast++;
      expectSameAsLossless(text);
    }
    // The generator exercises both paths.
    expect(fast).toBeGreaterThan(300);
    expect(fast).toBeLessThan(2700);
  });
});
