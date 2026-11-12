import { fireEvent, render, screen, within } from "@testing-library/react";
import { vi } from "vitest";
import { AuthAdministrationPanel } from "../components/auth-administration-panel";
import { AuthRecordsPanel } from "./auth-records-panel";
import { ServiceTokensPage } from "./service-tokens-page";
import { sessionWrapper, scriptedAdmin } from "../test/harness";

const realm = { id: "realm-members", name: "members", registrationEnabled: false, profileSchema: {} };
const policy = { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 1024, passwordPattern: "", identityFields: ["email"], mfaDurationSeconds: 300, mfaRule: "", mfaEnabled: false, otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5 };

it("keeps auth drafts mounted across keyboard-accessible configuration tabs", async () => {
  const h = scriptedAdmin();
  h.backend.authCollections.getAdministration = vi.fn(async () => ({ policy, providers: [], templates: [] }));
  render(<AuthAdministrationPanel collectionId={realm.id} />, { wrapper: sessionWrapper(h.backend, h.history) });
  fireEvent.click(await screen.findByText("Authentication settings"));
  const methods = screen.getByRole("tab", { name: "Auth methods" });
  fireEvent.change(screen.getByLabelText("Password pattern"), { target: { value: "[A-Z]" } });
  fireEvent.keyDown(methods, { key: "ArrowRight" });
  expect(screen.getByRole("tab", { name: "OAuth providers" })).toHaveFocus();
  expect(screen.getByRole("tabpanel")).toHaveAccessibleName("OAuth providers");
  fireEvent.change(screen.getByLabelText("OIDC issuer"), { target: { value: "https://login.example.test" } });
  fireEvent.keyDown(screen.getByRole("tab", { name: "OAuth providers" }), { key: "Home" });
  expect(screen.getByLabelText("Password pattern")).toHaveValue("[A-Z]");
  fireEvent.click(screen.getByRole("tab", { name: "OAuth providers" }));
  expect(screen.getByLabelText("OIDC issuer")).toHaveValue("https://login.example.test");
  expect(screen.getByRole("button", { name: "Reload authentication settings" })).toBeDisabled();
});

it("finds an email-bearing auth record by its ID without a server mutation", async () => {
  const h = scriptedAdmin();
  h.backend.authCollections.listAuthCollections = vi.fn(async () => [realm]);
  const list = vi.fn(async () => [{ id: "member-unique-42", authCollectionId: realm.id, email: "person@example.test", active: true, verified: true, emailVisibility: false, fields: {}, createdAt: "2026-11-12T00:00:00Z", updatedAt: "2026-11-12T00:00:00Z" }]);
  h.backend.authCollections.records = { list, get: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn() };
  render(<AuthRecordsPanel />, { wrapper: sessionWrapper(h.backend, h.history) });
  await screen.findByRole("option", { name: "members" });
  fireEvent.change(screen.getByLabelText("Authentication collection"), { target: { value: realm.id } });
  await screen.findByText("person@example.test");
  fireEvent.change(screen.getByLabelText("Find loaded users"), { target: { value: "  MEMBER-UNIQUE-42  " } });
  expect(within(screen.getByRole("table")).getByText("person@example.test")).toBeVisible();
  expect(list).toHaveBeenCalledTimes(1);
});

it("rejects fractional service-token days before issuing a credential", async () => {
  const h = scriptedAdmin();
  h.backend.serviceTokens.listServiceTokens = vi.fn(async () => []);
  const issue = vi.fn(); h.backend.serviceTokens.issueServiceToken = issue;
  render(<ServiceTokensPage />, { wrapper: sessionWrapper(h.backend, h.history) });
  await screen.findByText("No service tokens yet. Issue a token above when you need server-to-server access.");
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: "export" } });
  fireEvent.change(screen.getByLabelText("Lifetime (days)"), { target: { value: "0.5" } });
  fireEvent.change(screen.getByLabelText("Scopes (comma separated UUID:action)"), { target: { value: "0199ab10-0000-7000-8000-000000000099:read" } });
  fireEvent.click(screen.getByRole("button", { name: "Issue token" }));
  expect(screen.getByRole("alert")).toHaveTextContent("one day to 365 days");
  expect(issue).not.toHaveBeenCalled();
});
