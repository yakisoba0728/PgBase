import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import * as api from "../api/authCollections";
import { ApiError } from "../api/http";
import { setLanguage } from "../i18n";
import { PolicyTab } from "./PolicyTab";
import { administrationQuery } from "./queries";

const policy: api.AuthPolicy = { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 128, passwordPattern: "", identityFields: ["email"], mfaEnabled: false, mfaDurationSeconds: 600, mfaRule: "", otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5, etag: "e1" };
const collection = { id: "c", name: "users", type: "auth", registrationEnabled: true, fields: [] } as unknown as api.AuthCollection;
const administration: api.AuthAdministration = { policy, providers: null, templates: null };

// Like the tab body: the form gets the cached administration, which a save replaces.
function Host() {
  const query = useQuery({ ...administrationQuery(collection.id), staleTime: Infinity });
  return query.data ? <PolicyTab collection={collection} administration={query.data} /> : null;
}

function renderPolicy() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(administrationQuery(collection.id).queryKey, administration);
  const router = createMemoryRouter([{ path: "*", element: <Host /> }]);
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>);
}
const identity = () => screen.getByRole("textbox", { name: /Identity fields/ });

beforeEach(() => setLanguage("en"));
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

it("is clean after a save the server normalises", async () => {
  vi.spyOn(api, "setAuthPolicy").mockResolvedValue({ ...policy, identityFields: ["email", "username"], etag: "e2" });
  renderPolicy();
  fireEvent.change(identity(), { target: { value: "email,username" } });
  expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
  fireEvent.submit(document.getElementById("auth-policy")!);
  await waitFor(() => expect(identity()).toHaveValue("email, username"));
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
});

it("clears the stale notice once the stored policy is reloaded", async () => {
  vi.spyOn(api, "setAuthPolicy").mockRejectedValue(new ApiError(412, "precondition_failed", "stale"));
  vi.spyOn(api, "getAuthAdministration").mockResolvedValue({ ...administration, policy: { ...policy, identityFields: ["username"], etag: "e3" } });
  renderPolicy();
  fireEvent.change(identity(), { target: { value: "email phone" } });
  fireEvent.submit(document.getElementById("auth-policy")!);
  fireEvent.click(await screen.findByRole("button", { name: "Load theirs (drop mine)" }));
  await waitFor(() => expect(identity()).toHaveValue("username"));
  expect(screen.queryByRole("button", { name: "Load theirs (drop mine)" })).not.toBeInTheDocument();
});
