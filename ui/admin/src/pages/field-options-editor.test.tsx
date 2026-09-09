import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { DraftField } from "../designer/collection-designer";
import { fieldOptionsInput } from "../designer/field-options";
import { FieldOptionsEditor } from "./field-options-editor";

function TextOptions() {
  const [field, setField] = useState<DraftField>({ name: "contact", type: "text", required: false, unique: false, indexed: false, hasDefault: false, defaultText: "" });
  return <><FieldOptionsEditor field={field} index={0} onEdit={edit => { if (edit.set === "options") setField({ ...field, options: edit.value }); }} /><output data-testid="options">{JSON.stringify(fieldOptionsInput(field.options))}</output></>;
}

describe("N4 text field options", () => {
  it("edits and clears generation, allow, and deny values", async () => {
    const user = userEvent.setup();
    render(<TextOptions />);
    fireEvent.change(screen.getByLabelText("Field 1 autogenerate pattern"), { target: { value: "user-[a-z]{8}@example[.]com" } });
    await user.selectOptions(screen.getByLabelText("Field 1 format"), "email");
    await user.type(screen.getByLabelText(/Field 1 allowed domains/), "example.com");
    await user.type(screen.getByLabelText(/Field 1 denied domains/), "private.example.com");
    expect(screen.getByTestId("options")).toHaveTextContent('"autogeneratePattern":"user-[a-z]{8}@example[.]com"');
    expect(screen.getByTestId("options")).toHaveTextContent('"allowedDomains":["example.com"]');
    expect(screen.getByTestId("options")).toHaveTextContent('"deniedDomains":["private.example.com"]');
    await user.clear(screen.getByLabelText(/Field 1 denied domains/));
    expect(screen.getByTestId("options")).not.toHaveTextContent("deniedDomains");
  });
});
