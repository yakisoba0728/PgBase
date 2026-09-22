import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { parseJSON, stringifyJSON } from "../backend/json";
import type { CollectionDefinition, ViewDefinition, ViewFilter } from "../backend/ports";
import { ViewDefinitionEditor } from "./view-definition-editor";

const sources: CollectionDefinition[] = [{
	name: "source", tableName: "source", fields: [
		{ name: "title", type: "text", required: false, unique: false, indexed: false, ordinal: 0 },
		{ name: "body", type: "editor", required: false, unique: false, indexed: false, ordinal: 1 },
		{ name: "score", type: "bigint", required: false, unique: false, indexed: false, ordinal: 2 },
		{ name: "published", type: "boolean", required: false, unique: false, indexed: false, ordinal: 3 },
		{ name: "amount", type: "numeric", required: false, unique: false, indexed: false, ordinal: 4 },
	],
}];

describe("ViewDefinitionEditor", () => {
	it("preserves a numeric filter outside the JavaScript number range during projection edits", () => {
		const view = parseJSON('{"source":"source","projection":["title"],"filter":{"op":"gte","field":"amount","value":1e400}}') as ViewDefinition;
		const onChange = vi.fn();
		render(<form aria-label="View editor"><ViewDefinitionEditor view={view} sources={sources} disabled={false} modeLocked onChange={onChange} /></form>);

		const input = screen.getByLabelText("Filter value") as HTMLInputElement;
		expect(input.value).toBe("1e400");
		expect(screen.getByRole("form", { name: "View editor" })).toBeValid();
		fireEvent.click(screen.getByRole("checkbox", { name: "Project body" }));
		expect(stringifyJSON(onChange.mock.calls[0][0])).toBe('{"source":"source","projection":["title","body"],"filter":{"op":"gte","field":"amount","value":1e400}}');

		fireEvent.change(input, { target: { value: "1e401" } });
		expect(stringifyJSON(onChange.mock.calls[1][0])).toBe('{"source":"source","projection":["title"],"filter":{"op":"gte","field":"amount","value":1e401}}');
	});

	it("locks an existing SQL view to SQL mode while allowing query edits", () => {
		const onChange = vi.fn();
		render(<ViewDefinitionEditor view={{ sql: "SELECT id FROM app.source" }} sources={[]} disabled={false} modeLocked onChange={onChange} />);

		expect(screen.getByRole("combobox", { name: "Definition mode" })).toBeDisabled();
		const query = screen.getByRole("textbox", { name: "SQL SELECT" });
		expect(query).toBeEnabled();
		fireEvent.change(query, { target: { value: "SELECT id, title FROM app.source" } });
		expect(onChange).toHaveBeenCalledWith({ sql: "SELECT id, title FROM app.source" });
	});

	it.each(["all", "any"] as const)("preserves an existing %s filter while editing projected fields", (op) => {
		const filter = parseJSON(`{"op":"${op}","args":[{"op":"eq","field":"title","value":"published"},{"op":"gte","field":"score","value":9007199254740993}]}`) as ViewFilter;
		const view: ViewDefinition = { source: "source", projection: ["title"], filter };
		const onChange = vi.fn();
		render(<form aria-label="View editor"><ViewDefinitionEditor view={view} sources={sources} disabled={false} modeLocked onChange={onChange} /></form>);

		expect(screen.getByLabelText("Preserved static filter")).toHaveTextContent('"value": 9007199254740993');
		expect(screen.queryByRole("combobox", { name: "Filter field" })).not.toBeInTheDocument();
		expect(screen.getByRole("form", { name: "View editor" })).toBeValid();
		fireEvent.click(screen.getByRole("checkbox", { name: "Project body" }));
		expect(stringifyJSON(onChange.mock.calls[0][0])).toBe(`{"source":"source","projection":["title","body"],"filter":{"op":"${op}","args":[{"op":"eq","field":"title","value":"published"},{"op":"gte","field":"score","value":9007199254740993}]}}`);
	});

	it.each([{ op: "eq", field: "title" }, { op: "ne", field: "published" }] as const)("preserves an existing $field $op null filter while editing projected fields", ({ op, field }) => {
		const view: ViewDefinition = { source: "source", projection: ["title"], filter: { op, field, value: null } };
		const onChange = vi.fn();
		render(<form aria-label="View editor"><ViewDefinitionEditor view={view} sources={sources} disabled={false} modeLocked onChange={onChange} /></form>);

		expect(screen.getByLabelText("Preserved static filter")).toHaveTextContent('"value": null');
		expect(screen.queryByLabelText("Filter value")).not.toBeInTheDocument();
		expect(screen.getByRole("form", { name: "View editor" })).toBeValid();
		fireEvent.click(screen.getByRole("checkbox", { name: "Project body" }));
		expect(onChange).toHaveBeenCalledWith({ source: "source", projection: ["title", "body"], filter: { op, field, value: null } });
	});

	it.each([
		{ action: "Replace filter", expected: { op: "eq", field: "", value: "" } },
		{ action: "Remove filter", expected: undefined },
	])("allows $action explicitly without submitting the preview form", ({ action, expected }) => {
		const view: ViewDefinition = { source: "source", projection: ["title"], filter: { op: "eq", field: "title", value: null } };
		const onChange = vi.fn();
		const onSubmit = vi.fn();
		const { rerender } = render(<form onSubmit={(event) => { event.preventDefault(); onSubmit(); }}><ViewDefinitionEditor view={view} sources={sources} disabled={false} modeLocked onChange={onChange} /></form>);

		fireEvent.click(screen.getByRole("button", { name: action }));
		expect(onChange).toHaveBeenCalledWith({ source: "source", projection: ["title"], filter: expected });
		expect(onSubmit).not.toHaveBeenCalled();

		rerender(<ViewDefinitionEditor view={view} sources={sources} disabled modeLocked onChange={onChange} />);
		expect(screen.getByRole("button", { name: action })).toBeDisabled();
	});

	it.each(["title", "body"])("keeps an empty %s filter valid while editing projected fields", (field) => {
		const view: ViewDefinition = { source: "source", projection: ["title"], filter: { op: "eq", field, value: "" } };
		const onChange = vi.fn();
		render(<form aria-label="View editor"><ViewDefinitionEditor view={view} sources={sources} disabled={false} modeLocked onChange={onChange} /></form>);

		expect(screen.getByRole("textbox", { name: "Filter value" })).toHaveValue("");
		expect(screen.getByRole("form", { name: "View editor" })).toBeValid();
		fireEvent.click(screen.getByRole("checkbox", { name: "Project body" }));
		expect(onChange).toHaveBeenCalledWith({ source: "source", projection: ["title", "body"], filter: { op: "eq", field, value: "" } });
	});
});
