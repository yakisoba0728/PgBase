import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router";
import { expect, it, vi } from "vitest";
import { ExtensionScreenPage, registerScreen } from "./extensions";

it("shows a retryable error when an extension screen fails to mount", () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  let calls = 0;
  registerScreen({ id: "broken", label: "Broken", mount: (element) => { if (++calls === 1) throw new Error("boom"); element.textContent = "mounted"; } });
  render(<MemoryRouter initialEntries={["/ext/broken"]}><Routes><Route path="/ext/:id/*" element={<ExtensionScreenPage />} /></Routes></MemoryRouter>);
  expect(console.error).toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  expect(screen.getByText("mounted")).toBeVisible();
  expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
});
