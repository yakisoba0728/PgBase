import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as collections from "../api/collections";
import * as system from "../api/system";
import { setLanguage } from "../i18n";
import { ServiceTokensView } from "./ServiceTokensView";

const token = { id: "t1", name: "deploy", active: true, scopes: [], expiresAt: "2099-01-01T00:00:00Z", createdAt: "2026-11-14T00:00:00Z" } as unknown as system.ServiceToken;

function renderAt(search: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <ServiceTokensView /> }], { initialEntries: [`/tokens${search}`] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
}

beforeEach(() => {
  setLanguage("en");
  vi.spyOn(system, "listServiceTokens").mockResolvedValue({ items: [token] });
  vi.spyOn(collections, "listCollections").mockResolvedValue({ version: 1, collections: {} });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("revokes one token with a plain confirmation", async () => {
  const revoke = vi.spyOn(system, "revokeServiceToken").mockResolvedValue(undefined);
  renderAt("?token=t1");
  const pane = within(await screen.findByRole("region", { name: "Service token" }));
  fireEvent.click(await pane.findByRole("button", { name: "Revoke" }));
  const dialog = within(screen.getByRole("dialog"));
  expect(dialog.queryByRole("textbox")).not.toBeInTheDocument();
  fireEvent.click(dialog.getByRole("button", { name: "Revoke" }));
  await waitFor(() => expect(revoke).toHaveBeenCalledWith("t1"));
});

it("says so when the linked token does not exist", async () => {
  renderAt("?token=missing");
  expect(await screen.findByText("Not found.")).toBeInTheDocument();
});

it("selects scopes by column and row, with indeterminate states and a filter that never submits", async () => {
  vi.spyOn(collections, "listCollections").mockResolvedValue({
    version: 1,
    collections: { posts: { id: "c1", name: "posts" }, users: { id: "c2", name: "users" } },
  } as unknown as Awaited<ReturnType<typeof collections.listCollections>>);
  const issue = vi.spyOn(system, "issueServiceToken");
  renderAt("?token=new");
  const readColumn = await screen.findByRole("checkbox", { name: "Read on every listed collection" }) as HTMLInputElement;
  fireEvent.click(readColumn);
  expect(screen.getByRole("checkbox", { name: "posts Read" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "users Read" })).toBeChecked();
  expect(screen.getByText("2 scopes selected")).toBeInTheDocument();

  fireEvent.click(screen.getByRole("checkbox", { name: "users Read" }));
  expect(readColumn.checked).toBe(false);
  expect(readColumn.indeterminate).toBe(true);
  const postsRow = screen.getByRole("checkbox", { name: "Every action on posts" }) as HTMLInputElement;
  expect(postsRow.indeterminate).toBe(true);
  fireEvent.click(postsRow);
  expect(postsRow.checked).toBe(true);
  expect(screen.getByRole("checkbox", { name: "posts Delete" })).toBeChecked();

  const filter = screen.getByRole("searchbox", { name: "Filter collections" });
  fireEvent.change(filter, { target: { value: "use" } });
  expect(screen.queryByRole("checkbox", { name: "posts Read" })).not.toBeInTheDocument();
  // The column box now covers only the shown row.
  fireEvent.click(screen.getByRole("checkbox", { name: "Delete on every listed collection" }));
  expect(screen.getByRole("checkbox", { name: "users Delete" })).toBeChecked();
  fireEvent.keyDown(filter, { key: "Enter" });
  fireEvent.change(filter, { target: { value: "" } });
  expect(screen.getByRole("checkbox", { name: "posts Delete" })).toBeChecked();
  expect(issue).not.toHaveBeenCalled();
});
