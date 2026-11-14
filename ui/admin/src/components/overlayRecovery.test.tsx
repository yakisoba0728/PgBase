import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { SplitView } from "./SplitView";
import { TaskDialog } from "./Dialog";
import { copyText } from "./CopyButton";
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
it("keeps inside dialog padding open and dismisses only an outside click", () => {
  const close = vi.fn();
  render(<TaskDialog open title="Secret" onClose={close}>one time</TaskDialog>);
  const dialog = screen.getByRole("dialog");
  vi.spyOn(dialog, "getBoundingClientRect").mockReturnValue({ left: 10, right: 110, top: 10, bottom: 110 } as DOMRect);
  fireEvent.click(dialog, { clientX: 20, clientY: 20 });
  expect(close).not.toHaveBeenCalled();
  // A text selection that starts inside and is released on the backdrop is not a dismissal.
  fireEvent.pointerDown(screen.getByText("one time"), { clientX: 20, clientY: 20 });
  fireEvent.click(dialog, { clientX: 120, clientY: 20 });
  expect(close).not.toHaveBeenCalled();
  fireEvent.pointerDown(dialog, { clientX: 120, clientY: 20 });
  fireEvent.click(dialog, { clientX: 120, clientY: 20 });
  expect(close).toHaveBeenCalledOnce();
});
it("makes the covered mobile list inert and restores focus when the pane closes", () => {
  vi.stubGlobal("innerWidth", 600);
  const list = <button>Open row</button>;
  const { rerender } = render(<SplitView list={list} pane={null} paneLabel="Details" onClose={() => {}} />);
  screen.getByRole("button", { name: "Open row" }).focus();
  rerender(<SplitView list={list} pane={<button>Close details</button>} paneLabel="Details" onClose={() => {}} />);
  expect(document.querySelector(".split-list")).toHaveAttribute("inert");
  expect(screen.getByRole("button", { name: "Close details" })).toHaveFocus();
  rerender(<SplitView list={list} pane={null} paneLabel="Details" onClose={() => {}} />);
  expect(screen.getByRole("button", { name: "Open row" })).toHaveFocus();
});
it("restores focus when a desktop pane closes too", () => {
  const list = <button>Open row</button>;
  const { rerender } = render(<SplitView list={list} pane={null} paneLabel="Details" onClose={() => {}} />);
  screen.getByRole("button", { name: "Open row" }).focus();
  rerender(<SplitView list={list} pane={<button>Close details</button>} paneLabel="Details" onClose={() => {}} />);
  screen.getByRole("button", { name: "Close details" }).focus();
  rerender(<SplitView list={list} pane={null} paneLabel="Details" onClose={() => {}} />);
  expect(screen.getByRole("button", { name: "Open row" })).toHaveFocus();
});
it("reports clipboard unavailability asynchronously", async () => {
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
  await act(async () => { await expect(copyText("private")).resolves.toBe(false); });
});
