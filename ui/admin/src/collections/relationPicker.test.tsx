import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { describe, expect, it, vi } from "vitest";
import type { Collection, CollectionField } from "../api/collections";
import { RelationPicker } from "./RelationPicker";

const createRecord = vi.fn();
vi.mock("../api/records", () => ({
  createRecord: (...args: unknown[]) => createRecord(...args),
  listRecords: async () => ({ items: [], nextCursor: undefined }),
}));

const companies = { id: "c1", name: "companies", tableName: "companies", createdAt: "", fields: [{ id: "f1", name: "name", columnName: "name", type: "text", required: false, unique: false, indexed: false, ordinal: 1, presentable: true }] } as Collection;
const field = { id: "r1", name: "company", type: "relation", required: false, relation: { targetCollectionId: "c1", cardinality: "multiple" } } as CollectionField;

describe("RelationPicker create", () => {
  it("creates once per double Enter, with an idempotency key", async () => {
    let finish: (value: unknown) => void = () => undefined;
    createRecord.mockReturnValue(new Promise((resolve) => (finish = resolve)));
    const client = new QueryClient();
    client.setQueryData(["collections"], { collections: { c1: companies } });
    const onChange = vi.fn();
    const onPendingChange = vi.fn();
    render(
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <RelationPicker onPendingChange={onPendingChange} field={field} value={[]} onChange={onChange} readOnly={false} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    const input = screen.getByRole("combobox");
    fireEvent.change(input, { target: { value: "Acme" } });
    await screen.findByText(/Acme/, { selector: ".relation-create span" }, { timeout: 2000 });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Enter" });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(createRecord).toHaveBeenCalled());
    fireEvent.keyDown(input, { key: "Enter" });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(onPendingChange).toHaveBeenCalledWith(true);
    expect(createRecord).toHaveBeenCalledTimes(1);
    expect(createRecord.mock.calls[0][2]).toMatchObject({ idempotencyKey: expect.stringMatching(/^[0-9a-f]{32}$/) });
    finish({ record: { id: "x1", name: "Acme" } });
    await waitFor(() => expect(onChange).toHaveBeenCalledWith(["x1"]));
  });
});
