import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as collections from "../api/collections";
import { ApiError } from "../api/http";
import * as users from "../api/users";
import { setLanguage } from "../i18n";
import { ShellOverlays } from "./CommandPalette";
import { setPalette } from "./shell";

let client: QueryClient;
beforeEach(() => {
  setLanguage("en");
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.spyOn(collections, "listCollections").mockResolvedValue({ collections: { members: { id: "auth1", name: "members", type: "auth", fields: [] } } } as unknown as collections.CollectionCatalog);
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
});
afterEach(() => {
  act(() => setPalette(false));
  cleanup();
  client.clear();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

function open() {
  const router = createMemoryRouter([{ path: "*", element: <ShellOverlays /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  act(() => setPalette(true));
  return { router, input: screen.getByRole("combobox") };
}

const type = async (input: HTMLElement, value: string) => {
  fireEvent.change(input, { target: { value } });
  await act(() => vi.advanceTimersByTimeAsync(300));
};

it("searches users after two characters, in their own group, and opens the user pane", async () => {
  const search = vi.spyOn(users, "listUsers").mockResolvedValue({ items: [{ id: "u-1", email: "ada@example.com", authCollectionId: "auth1" } as users.ManagedUser] });
  const { router, input } = open();
  await type(input, "a");
  expect(search).not.toHaveBeenCalled();
  await type(input, "ada");
  expect(search).toHaveBeenCalledTimes(1);
  expect(search).toHaveBeenCalledWith(expect.objectContaining({ q: "ada", limit: 5 }));
  const option = await screen.findByRole("option", { name: /ada@example\.com/ });
  expect(option.textContent).toContain("members");
  expect(screen.getByText("Users")).toBeTruthy();
  fireEvent.click(option);
  expect(router.state.location.pathname + router.state.location.search).toBe("/auth/users?user=u-1");
});

it("shows a loading row, then an error row that is not an option", async () => {
  let fail: (error: unknown) => void = () => {};
  vi.spyOn(users, "listUsers").mockImplementation(() => new Promise((_, reject) => (fail = reject)));
  const { input } = open();
  await type(input, "zz-nobody");
  expect(screen.getByRole("status").textContent).toBe("Searching users…");
  expect(screen.queryByText("Nothing matches.")).toBeNull();
  await act(async () => fail(new ApiError(403, "forbidden", "Not allowed")));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toMatch(/^Couldn’t search users: /);
  expect(screen.queryAllByRole("option").some((option) => option.textContent?.includes("search users"))).toBe(false);
});
