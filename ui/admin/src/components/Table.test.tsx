import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Row, Table, TableScroll } from "./Table";

afterEach(cleanup);

function grid(onOpen: () => void, current = false) {
  return render(
    <TableScroll>
      <Table label="Jobs">
        <tbody>
          <Row onOpen={onOpen} current={current}>
            <td><a href="#x">link</a></td>
            <td><button type="button">act</button></td>
            <td>plain</td>
          </Row>
        </tbody>
      </Table>
    </TableScroll>,
  );
}

it("renders the scroll area and the named grid", () => {
  const { container } = grid(() => {});
  expect(container.firstElementChild).toHaveClass("grid-scroll");
  expect(screen.getByRole("table", { name: "Jobs" })).toHaveClass("grid");
});

it("opens on a plain click or Enter/Space on the row, marks the current row", () => {
  const open = vi.fn();
  grid(open, true);
  const row = screen.getByText("plain").closest("tr")!;
  expect(row).toHaveAttribute("aria-current", "true");
  expect(row).toHaveAttribute("tabindex", "0");
  fireEvent.click(screen.getByText("plain"));
  fireEvent.keyDown(row, { key: "Enter" });
  fireEvent.keyDown(row, { key: " " });
  expect(open).toHaveBeenCalledTimes(3);
});

it("leaves modifier clicks, inner links and buttons, and keys in them alone", () => {
  const open = vi.fn();
  grid(open);
  fireEvent.click(screen.getByText("plain"), { metaKey: true });
  fireEvent.click(screen.getByText("plain"), { ctrlKey: true });
  fireEvent.click(screen.getByText("plain"), { button: 1 });
  fireEvent.click(screen.getByText("link"));
  fireEvent.click(screen.getByText("act"));
  fireEvent.keyDown(screen.getByText("act"), { key: "Enter" });
  expect(open).not.toHaveBeenCalled();
});

it("does not open when a click ends a text selection inside the row", () => {
  const open = vi.fn();
  grid(open);
  const cell = screen.getByText("plain");
  const range = document.createRange();
  range.selectNodeContents(cell);
  const selection = window.getSelection()!;
  selection.removeAllRanges();
  selection.addRange(range);
  try {
    fireEvent.click(cell);
    expect(open).not.toHaveBeenCalled();
  } finally {
    selection.removeAllRanges();
  }
  fireEvent.click(cell);
  expect(open).toHaveBeenCalledOnce();
});
