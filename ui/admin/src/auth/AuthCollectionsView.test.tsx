import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import * as api from "../api/authCollections";
import { setLanguage } from "../i18n";
import { AuthCollectionsIndex, AuthCollectionView } from "./AuthCollectionsView";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("leaves the current tab alone on a Cmd/Ctrl+click of a collection link", async () => {
  setLanguage("en");
  vi.spyOn(api, "listAuthCollections").mockResolvedValue([{ id: "c", name: "members", type: "auth", registrationEnabled: true, fields: [] } as unknown as api.AuthCollection]);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <AuthCollectionsIndex /> }], { initialEntries: ["/auth/collections"] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
  fireEvent.click(await screen.findByRole("link", { name: "members" }), { metaKey: true });
  expect(router.state.location.pathname).toBe("/auth/collections");
});

const policy: api.AuthPolicy = { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 128, passwordPattern: "", identityFields: ["email"], mfaEnabled: false, mfaDurationSeconds: 600, mfaRule: "", otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5, etag: "e1" };

function renderCollection(collection: api.AuthCollection) {
  setLanguage("en");
  vi.spyOn(api, "listAuthCollections").mockResolvedValue([collection]);
  vi.spyOn(api, "getAuthAdministration").mockResolvedValue({ policy, providers: null, templates: null });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <AuthCollectionView name={collection.name} /> }], { initialEntries: [`/auth/collections/${collection.name}`] });
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
}

it("deletes a collection from the danger zone below its policy, after its name is typed", async () => {
  renderCollection({ id: "c", name: "members", type: "auth", registrationEnabled: true, fields: [] } as unknown as api.AuthCollection);
  const zone = within(await screen.findByRole("region", { name: "Danger zone" }));
  fireEvent.click(zone.getByRole("button", { name: "Delete collection" }));
  const dialog = within(screen.getByRole("dialog"));
  expect(dialog.getByRole("button", { name: "Delete" })).toBeDisabled();
  fireEvent.change(dialog.getByRole("textbox", { name: "Type members to confirm" }), { target: { value: "members" } });
  expect(dialog.getByRole("button", { name: "Delete" })).toBeEnabled();
});

it("has no danger zone for the built-in users collection", async () => {
  renderCollection({ id: "00000000-0000-0000-0000-000000000001", name: "users", type: "auth", registrationEnabled: true, fields: [] } as unknown as api.AuthCollection);
  expect(await screen.findByRole("textbox", { name: /Identity fields/ })).toBeInTheDocument();
  expect(screen.queryByRole("region", { name: "Danger zone" })).not.toBeInTheDocument();
});
