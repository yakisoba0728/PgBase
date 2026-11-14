import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it } from "vitest";
import { setLanguage } from "../i18n";
import { newField, type DraftField } from "./draft";
import { FieldRows } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";
afterEach(cleanup);

function Harness() {
  const [fields, setFields] = useState<DraftField[]>(() => ["a", "b", "c"].map((name) => ({ ...newField("text", []), name })));
  const editor = {
    draft: { fields, recordId: { kind: "uuid" }, indexes: [] },
    base: { fields, indexes: [] },
    errors: { fields: {}, indexes: {} },
    setPane: () => {},
    moveField: (key: string, step: -1 | 1) =>
      setFields((current) => {
        const next = [...current];
        const from = next.findIndex((field) => field.key === key);
        if (next[from + step]) [next[from], next[from + step]] = [next[from + step], next[from]];
        return next;
      }),
  } as unknown as SchemaEditor;
  return <FieldRows editor={editor} />;
}

const order = () => [...document.querySelectorAll("[data-field-key] .schema-name")].map((name) => name.textContent);

it("moves the focused field with Alt+Arrow and keeps focus on it", async () => {
  setLanguage("en");
  render(<Harness />);
  const row = () => screen.getByText("a").closest("button")!;
  row().focus();
  for (let press = 0; press < 2; press++) {
    fireEvent.keyDown(document.activeElement!, { key: "ArrowDown", altKey: true });
    await act(() => new Promise((resolve) => requestAnimationFrame(resolve)));
  }
  expect(order()).toEqual(["b", "c", "a"]);
  expect(document.activeElement).toBe(row());
  fireEvent.keyDown(document.activeElement!, { key: "ArrowDown" });
  expect(order()).toEqual(["b", "c", "a"]);
});
