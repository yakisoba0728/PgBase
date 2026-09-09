import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ViewDefinitionEditor } from "./view-definition-editor";

describe("ViewDefinitionEditor", () => {
	it("locks an existing SQL view to SQL mode while allowing query edits", () => {
		const onChange = vi.fn();
		render(<ViewDefinitionEditor view={{ sql: "SELECT id FROM app.source" }} sources={[]} disabled={false} modeLocked onChange={onChange} />);

		expect(screen.getByRole("combobox", { name: "Definition mode" })).toBeDisabled();
		const query = screen.getByRole("textbox", { name: "SQL SELECT" });
		expect(query).toBeEnabled();
		fireEvent.change(query, { target: { value: "SELECT id, title FROM app.source" } });
		expect(onChange).toHaveBeenCalledWith({ sql: "SELECT id, title FROM app.source" });
	});
});
