import { isSafeNumber, LosslessNumber, parse, stringify } from "lossless-json";

export function parseJSON(text: string): unknown {
  return parse(text, null, {
    parseNumber: (value) => isSafeNumber(value) ? Number(value) : new LosslessNumber(value),
    onDuplicateKey: ({ key }) => { throw new SyntaxError(`Duplicate JSON key: ${key}`); },
  });
}

export function stringifyJSON(value: unknown, space?: number): string {
  const encoded = stringify(value, null, space);
  if (encoded === undefined) throw new TypeError("Value cannot be represented as JSON");
  return encoded;
}
