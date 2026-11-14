import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as api from "../api/logs";
import { setLanguage } from "../i18n";
import { LogsActions } from "./LogsView";

beforeEach(() => setLanguage("en"));
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("deletes every request log only after the screen's name is typed", async () => {
  vi.spyOn(api, "getRequestLogStatistics").mockResolvedValue({ total: 3 } as unknown as api.RequestLogStatistics);
  const clear = vi.spyOn(api, "clearRequestLogs").mockResolvedValue(undefined);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <LogsActions /> }], { initialEntries: ["/logs"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Delete all" }));
  const dialog = within(screen.getByRole("dialog"));
  const confirm = dialog.getByRole("button", { name: "Delete all" });
  expect(confirm).toBeDisabled();
  fireEvent.change(dialog.getByRole("textbox", { name: "Type Logs to confirm" }), { target: { value: "Logs" } });
  fireEvent.click(confirm);
  await waitFor(() => expect(clear).toHaveBeenCalled());
});
