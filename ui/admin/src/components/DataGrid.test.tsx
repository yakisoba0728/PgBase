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
});
