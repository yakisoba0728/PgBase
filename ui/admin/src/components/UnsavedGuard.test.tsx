import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { setLanguage } from "../i18n";
import { UnsavedGuard } from "./UnsavedGuard";
afterEach(cleanup);
it("protects active edits even when a preserved draft allows the navigation", async () => {
  setLanguage("en");
  const discard = vi.fn();
  const router = createMemoryRouter([{ path: "*", element: <><UnsavedGuard dirty onDiscard={discard} /><UnsavedGuard dirty leaves={() => false} /></> }], { initialEntries: ["/rules"] });
  render(<RouterProvider router={router} />);
  await act(async () => { void router.navigate("/schema"); });
  await waitFor(() => expect(screen.getByRole("dialog")).toHaveAttribute("open"));
  expect(router.state.location.pathname).toBe("/rules");
  fireEvent.click(screen.getByRole("button", { name: /discard/i }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/schema"));
  expect(discard).toHaveBeenCalledOnce();
});
