import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as collections from "../api/authCollections";
import { ApiError } from "../api/http";
import * as users from "../api/users";
import { setLanguage } from "../i18n";
import { UsersView } from "./UsersView";

const at = "2026-11-13T00:00:00Z";
const user: users.ManagedUser = { id: "00000000-0000-4000-8000-000000000010", authCollectionId: "c", email: "a@example.test", verified: true, active: true, createdAt: at, updatedAt: at };
const collection = { id: "c", name: "users", type: "auth", registrationEnabled: true, fields: [] } as unknown as collections.AuthCollection;
const record: collections.AuthRecord = { id: user.id, authCollectionId: "c", email: user.email, emailVisibility: false, verified: true, active: true, fields: {}, createdAt: at, updatedAt: at };

function renderUsers() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <UsersView /> }], { initialEntries: [`/users?user=${user.id}`] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  return router;
}

beforeEach(() => {
  setLanguage("en");
  vi.spyOn(users, "listUsers").mockResolvedValue({ items: [user], total: 1 });
  vi.spyOn(users, "listUserOAuthIdentities").mockResolvedValue([]);
  vi.spyOn(collections, "listAuthCollections").mockResolvedValue([collection]);
  vi.spyOn(collections, "listAuthRecords").mockResolvedValue({ items: [record] } as Awaited<ReturnType<typeof collections.listAuthRecords>>);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("closes the pane after deleting a user with unsaved profile edits", async () => {
  vi.spyOn(users, "getUser").mockResolvedValue(user);
  const remove = vi.spyOn(collections, "deleteAuthRecord").mockResolvedValue(undefined);
  const router = renderUsers();
  const pane = within(await screen.findByRole("region", { name: "User" }));
  fireEvent.change(await pane.findByRole("textbox", { name: /^Email/ }), { target: { value: "b@example.test" } });
  fireEvent.click(pane.getByRole("button", { name: "Delete user" }));
  const dialog = within(screen.getByRole("dialog"));
  fireEvent.change(dialog.getByRole("textbox", { name: `Type ${user.email} to confirm` }), { target: { value: user.email } });
  fireEvent.click(dialog.getByRole("button", { name: "Delete" }));
  await waitFor(() => expect(router.state.location.search).not.toContain("user="));
  expect(remove).toHaveBeenCalledWith("c", user.id);
  expect(screen.queryByText("Discard unsaved changes?")).not.toBeInTheDocument();
});

it("says the user is gone instead of showing the listed row", async () => {
  vi.spyOn(users, "getUser").mockRejectedValue(new ApiError(404, "not_found", "gone"));
  renderUsers();
  expect(await screen.findByText("Not found.")).toBeInTheDocument();
});

it("revokes every session of a user only after the email is typed", async () => {
  vi.spyOn(users, "getUser").mockResolvedValue(user);
  const revoke = vi.spyOn(users, "revokeUserSessions").mockResolvedValue(2);
  renderUsers();
  const pane = within(await screen.findByRole("region", { name: "User" }));
  fireEvent.click(await pane.findByRole("button", { name: "Revoke sessions" }));
  const dialog = within(screen.getByRole("dialog"));
  const confirm = dialog.getByRole("button", { name: "Revoke sessions" });
  expect(confirm).toBeDisabled();
  fireEvent.change(dialog.getByRole("textbox", { name: `Type ${user.email} to confirm` }), { target: { value: user.email } });
  fireEvent.click(confirm);
  await waitFor(() => expect(revoke).toHaveBeenCalledWith(user.id));
});

it("keeps Delete on the left of the pane footer, beside Save", async () => {
  vi.spyOn(users, "getUser").mockResolvedValue(user);
  renderUsers();
  const pane = within(await screen.findByRole("region", { name: "User" }));
  const remove = await pane.findByRole("button", { name: "Delete user" });
  expect(remove.closest("footer")).toBe(pane.getByRole("button", { name: "Save" }).closest("footer"));
  expect(remove).toHaveClass("btn-danger", "pane-footer-start");
});

it("keeps at most ten pages, offers a way back to the start and still shows a user opened from a dropped page", async () => {
  const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
  vi.spyOn(users, "listUsers").mockImplementation(async (options) => {
    const after = options?.after;
    const page = after === undefined ? 0 : Number(after.slice(-12)) / 10;
    const items = Array.from({ length: 10 }, (_, i) => ({ ...user, id: uuid(page * 10 + i + 1), email: `u${page * 10 + i + 1}@example.test` }));
    return { items, nextAfter: uuid(page * 10 + 10), total: after === undefined ? 500 : undefined };
  });
  vi.spyOn(users, "getUser").mockResolvedValue(user);
  Element.prototype.scrollTo = vi.fn();
  renderUsers();
  expect(await screen.findByText("u1@example.test")).toBeInTheDocument();
  for (let page = 1; page <= 10; page++) {
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    await screen.findByText(`u${page * 10 + 1}@example.test`);
  }
  expect(screen.queryByText("u1@example.test")).not.toBeInTheDocument();
  expect(screen.getAllByRole("row")).toHaveLength(101);
  expect(screen.getByText(/500 users/)).toBeInTheDocument();
  expect(within(screen.getByRole("region", { name: "User" })).getByRole("textbox", { name: /^Email/ })).toHaveValue(user.email);
  fireEvent.click(screen.getByRole("button", { name: "Back to start" }));
  expect(await screen.findByText("u1@example.test")).toBeInTheDocument();
  await waitFor(() => expect(screen.queryByRole("button", { name: "Back to start" })).not.toBeInTheDocument());
});
