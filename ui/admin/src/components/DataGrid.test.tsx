import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { DataGrid } from "./DataGrid";

describe("DataGrid", () => {
  it("keeps exact text, marks NULL and numbers, and titles long cells", () => {
    const long = "x".repeat(60);
    render(
      <DataGrid
        label="result"
        columns={[{ name: "n", type: "numeric" }, { name: "v" }]}
        rows={[[{ text: "12345678901234567890.50", numeric: true }, { text: null }], [{ text: "1" }, { text: long, muted: true }]]}
      />,
    );
    expect(screen.getByRole("table", { name: "result" })).toBeInTheDocument();
    expect(screen.getByText("12345678901234567890.50")).toHaveClass("num");
    expect(screen.getByText("NULL")).toHaveClass("data-grid-null");
    expect(screen.getByText(long)).toHaveAttribute("title", long);
    expect(screen.getByText(long)).toHaveAttribute("data-muted");
  });

  it("renders a slice with spacers, row indexes and fixed column widths", () => {
    const { container } = render(
      <DataGrid label="big" columns={[{ name: "n" }]} rows={[[{ text: "a" }], [{ text: "b" }]]} virtual={{ start: 10, total: 600, before: 400, after: 23520 }} widths={["12em"]} />,
    );
    expect(screen.getByRole("table", { name: "big" })).toHaveAttribute("aria-rowcount", "601");
    expect(screen.getByText("a").closest("tr")).toHaveAttribute("aria-rowindex", "12");
    const spacers = container.querySelectorAll("tbody tr[aria-hidden] td");
    expect([...spacers].map((cell) => (cell as HTMLElement).style.height)).toEqual(["400px", "23520px"]);
    expect(screen.getByRole("columnheader", { name: "n" }).style.minWidth).toBe("12em");
  });

  it("adds no row metadata without a slice", () => {
    render(<DataGrid label="small" columns={[{ name: "n" }]} rows={[[{ text: "a" }]]} />);
    expect(screen.getByRole("table", { name: "small" })).not.toHaveAttribute("aria-rowcount");
    expect(screen.getByText("a").closest("tr")).not.toHaveAttribute("aria-rowindex");
  });
});
