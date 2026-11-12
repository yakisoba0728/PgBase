import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { AuthRecordsPanel } from "./pages/auth-records-panel";
import { AuthTokenPolicies } from "./components/auth-token-policies";
import { AuthCollectionSchema } from "./components/auth-collection-schema";
import { RealtimePanel } from "./pages/realtime-panel";
import { UnsavedDrafts, draftLedger } from "./routing/unsaved-draft";
import { SettingsPage } from "./pages/settings-page";
import { scriptedAdmin, sessionWrapper, textField, renderAdmin } from "./test/harness";
import type { AdminSettings, AuthRecord, AuthRecordsPort } from "./backend/ports";
import { CollectionCatalog } from "./backend/ports";
import { collectionChange, initial, loadedState } from "./designer/collection-designer-state";
const realm = { id: "realm-1", name: "members", registrationEnabled: false, profileSchema: {}, fields: [{ name: "nickname", type: "text" as const, required: false, indexed: false, unique: false }], rules: { list: null, view: "", create: null, update: null, delete: null, auth: "", manage: null } };
const record: AuthRecord = { id: "member-1", authCollectionId: realm.id, email: "member@example.test", active: true, verified: false, emailVisibility: false, fields: { nickname: "Current" }, createdAt: "2026-10-24T00:00:00Z", updatedAt: "2026-10-24T00:00:00Z" };
function recordsHarness() {
  const h = scriptedAdmin("/admin/users"); let rows: AuthRecord[] = [];
  h.backend.authCollections.listAuthCollections = vi.fn(async () => [realm]);
  const port: AuthRecordsPort = {
    list: vi.fn(async () => rows), get: vi.fn(async () => record),
    create: vi.fn(async (_realm, input) => { const saved = { ...record, ...input }; rows = [saved]; return saved; }),
    update: vi.fn(async (_realm, _id, input) => ({ ...record, ...input })), remove: vi.fn(async () => { rows = []; }),
  };
  h.backend.authCollections.records = port;
  return { h, port, show: () => render(<AuthRecordsPanel />, { wrapper: sessionWrapper(h.backend, h.history) }) };
}
async function openCreate() {
  await screen.findByRole("option", { name: "members" });
  fireEvent.change(screen.getByLabelText("Authentication collection"), { target: { value: realm.id } });
  fireEvent.click(await screen.findByRole("button", { name: "Create user" }));
  await screen.findByLabelText("User email");
}
it("creates collection users, refreshes the list, then reads fresh detail before editing", async () => {
  const { h, port, show } = recordsHarness(); show(); await openCreate();
  fireEvent.change(screen.getByLabelText("User email"), { target: { value: "new@example.test" } });
  fireEvent.change(screen.getByLabelText("Initial password"), { target: { value: "synthetic password for test" } });
  fireEvent.change(screen.getByLabelText("Profile fields JSON"), { target: { value: '{"nickname":"New"}' } });
  fireEvent.click(screen.getByRole("button", { name: "Save user" }));
  await screen.findByText("new@example.test"); expect(port.create).toHaveBeenCalledWith(realm.id, expect.objectContaining({ fields: { nickname: "New" } }), expect.any(AbortSignal));
  fireEvent.click(screen.getByRole("button", { name: "Edit user" }));
  await waitFor(() => expect(screen.getByLabelText("Profile fields JSON")).toHaveValue('{\n  "nickname": "Current"\n}'));
  expect(port.get).toHaveBeenCalledWith(realm.id, record.id, expect.any(AbortSignal));
  expect(h.history.path()).toBe("/admin/users");
});
it("keeps an unconfirmed auth-record draft and never automatically retries the write", async () => {
  const { port, show } = recordsHarness(); vi.mocked(port.create).mockRejectedValue(new Error("connection lost")); show(); await openCreate();
  fireEvent.change(screen.getByLabelText("User email"), { target: { value: record.email } }); fireEvent.change(screen.getByLabelText("Initial password"), { target: { value: "synthetic test password" } }); fireEvent.click(screen.getByRole("button", { name: "Save user" }));
  await screen.findByRole("alert"); expect(screen.getByLabelText("User email")).toHaveValue(record.email); expect(port.create).toHaveBeenCalledTimes(1);
});
it("rejects invalid profile JSON before sending an auth-record write", async () => {
  const { port, show } = recordsHarness(); show(); await openCreate(); fireEvent.change(screen.getByLabelText("User email"), { target: { value: record.email } }); fireEvent.change(screen.getByLabelText("Initial password"), { target: { value: "synthetic test password" } }); fireEvent.change(screen.getByLabelText("Profile fields JSON"), { target: { value: "[]" } }); fireEvent.click(screen.getByRole("button", { name: "Save user" })); await screen.findByText("Profile fields must be a JSON object."); expect(port.create).not.toHaveBeenCalled();
});
it("requires the exact typed user identity before permanent auth-record deletion", async () => {
  const { port, show } = recordsHarness(); vi.mocked(port.list).mockResolvedValue([record]); show(); await screen.findByRole("option", { name: "members" }); fireEvent.change(screen.getByLabelText("Authentication collection"), { target: { value: realm.id } }); fireEvent.click(await screen.findByRole("button", { name: "Edit user" })); await screen.findByLabelText("User email"); fireEvent.click(screen.getByRole("button", { name: "Delete user" }));
  const confirm = screen.getByRole("button", { name: "Delete user permanently" }); expect(confirm).toBeDisabled(); expect(port.remove).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText(`Type ${record.email} to confirm delete user permanently`), { target: { value: record.email } }); fireEvent.click(confirm); await waitFor(() => expect(port.remove).toHaveBeenCalledWith(realm.id, record.id, expect.any(AbortSignal)));
});
it("keeps policy drafts while collapsed and changes lifetime without resending a signing key", async () => {
  const h = scriptedAdmin(); h.backend.authCollections.tokenPolicies = vi.fn(async () => [{ purpose: "session", ttlSeconds: 3600, keyVersion: "v1", keyConfigured: true }]); const put = vi.fn(async (_id, purpose, value) => ({ purpose, ttlSeconds: value.ttlSeconds, keyVersion: "v1", keyConfigured: true })); h.backend.authCollections.putTokenPolicy = put;
  render(<AuthTokenPolicies collectionId={realm.id} />, { wrapper: sessionWrapper(h.backend, h.history) }); const u = userEvent.setup(); await u.click(screen.getByText("Token policies")); const group = await screen.findByRole("group", { name: "Session" }); fireEvent.change(within(group).getByLabelText(/Token lifetime/), { target: { value: "7200" } }); expect(screen.getByRole("button", { name: "Reload token policies" })).toBeDisabled(); await u.click(screen.getByText("Token policies")); await u.click(screen.getByText("Token policies")); expect(within(group).getByLabelText(/Token lifetime/)).toHaveValue(7200); fireEvent.click(within(group).getByRole("button", { name: "Save token policy" })); await waitFor(() => expect(put).toHaveBeenCalledWith(realm.id, "session", { ttlSeconds: 7200 }, expect.any(AbortSignal)));
});
it("does not rotate token keys until the purpose is explicitly confirmed", async () => {
  const h = scriptedAdmin(); h.backend.authCollections.tokenPolicies = vi.fn(async () => [{ purpose: "session", ttlSeconds: 3600, keyVersion: "v1", keyConfigured: true }]); const put = vi.fn(async () => ({ purpose: "session", ttlSeconds: 3600, keyVersion: "v2", keyConfigured: true })); h.backend.authCollections.putTokenPolicy = put;
  render(<AuthTokenPolicies collectionId={realm.id} />, { wrapper: sessionWrapper(h.backend, h.history) }); await userEvent.click(screen.getByText("Token policies")); const group = await screen.findByRole("group", { name: "Session" }); fireEvent.click(within(group).getByLabelText("Replace signing key")); fireEvent.change(within(group).getByLabelText("New key version"), { target: { value: "v2" } }); fireEvent.change(within(group).getByLabelText("New signing key"), { target: { value: "a".repeat(32) } }); fireEvent.click(within(group).getByRole("button", { name: "Save token policy" })); expect(put).not.toHaveBeenCalled(); fireEvent.change(within(group).getByLabelText("Type session to confirm replace signing key"), { target: { value: "session" } }); fireEvent.click(within(group).getByRole("button", { name: "Replace signing key" })); await waitFor(() => expect(put).toHaveBeenCalledWith(realm.id, "session", { ttlSeconds: 3600, keyVersion: "v2", key: "a".repeat(32) }, expect.any(AbortSignal)));
});
it("preserves auth field constraints and locked rules when editing structured profile fields", async () => {
  const h = scriptedAdmin(); h.backend.schema.catalog = vi.fn(async () => new CollectionCatalog(1, [])); const update = vi.fn(async (_id, input) => ({ ...realm, ...input })); h.backend.authCollections.updateAuthCollection = update;
  render(<AuthCollectionSchema collection={realm} onSaved={vi.fn()} />, { wrapper: sessionWrapper(h.backend, h.history) }); await userEvent.click(screen.getByText("Fields, access rules and record identity")); const input = await screen.findByLabelText("Field 1 name"); fireEvent.change(input, { target: { value: "display_name" } }); fireEvent.click(screen.getByLabelText(/Confirm removal of field definitions/)); fireEvent.click(screen.getByRole("button", { name: "Save fields and rules" })); await waitFor(() => expect(update).toHaveBeenCalledWith(realm.id, expect.objectContaining({ fields: [expect.objectContaining({ name: "display_name", unique: false })], rules: realm.rules }), expect.any(AbortSignal)));
});
it("includes existing advanced metadata in previews and does not copy an alias onto a duplicated collection", () => {
  const definition = { id: "articles", name: "articles", tableName: "articles", fields: [textField("title", 0)], compatibilityName: "legacy_articles", validationMode: "pocketbase" as const };
  const catalog = new CollectionCatalog(3, [definition]); const edit = initial({ kind: "edit", collection: "articles" }, "edit"); expect(collectionChange({ ...edit, ...loadedState(edit, catalog) })).toMatchObject({ compatibilityName: "legacy_articles", validationMode: "pocketbase" });
  const duplicate = initial({ kind: "duplicate", collection: "articles" }, "copy"); expect(collectionChange({ ...duplicate, ...loadedState(duplicate, catalog) })).toMatchObject({ validationMode: "pocketbase" }); expect(collectionChange({ ...duplicate, ...loadedState(duplicate, catalog) })).not.toHaveProperty("compatibilityName");
});
it("validates realtime event identity and refreshes status after retry", async () => {
  const port = { status: vi.fn(async () => ({ pending: 1, dead: 2, delivered: 3, cursorFloor: 4 })), retry: vi.fn(async () => {}) }; render(<RealtimePanel port={port} />); await screen.findByText("Failed"); expect(screen.getByRole("button", { name: "Retry event" })).toBeDisabled(); fireEvent.change(screen.getByLabelText("Event ID"), { target: { value: "bad-id" } }); expect(screen.getByRole("button", { name: "Retry event" })).toBeDisabled(); fireEvent.change(screen.getByLabelText("Event ID"), { target: { value: "00000000-0000-4000-8000-000000000001" } }); fireEvent.click(screen.getByRole("button", { name: "Retry event" })); await screen.findByText(/Retry requested/); expect(port.retry).toHaveBeenCalledTimes(1); await waitFor(() => expect(port.status).toHaveBeenCalledTimes(2));
});
it("uses the selected page query for continuation and resets old cursors when query options change", async () => {
  const h = scriptedAdmin("/admin/collections/articles/records"); h.backend.schema.catalog = vi.fn(async () => new CollectionCatalog(1, [{ name: "articles", tableName: "articles", fields: [textField("title",0)] }])); const list = vi.fn(async () => ({ items: [{ id: "row-1", title: "Hello" }], nextCursor: "continuation" })); h.backend.records.list = list; renderAdmin(h); await screen.findByText("Hello"); await userEvent.click(screen.getByText("Query options")); fireEvent.change(screen.getByLabelText("Records per page"), { target: { value: "25" } }); fireEvent.change(screen.getByLabelText("Server fields"), { target: { value: "title" } }); fireEvent.change(screen.getByLabelText("Expand relations"), { target: { value: "author" } }); fireEvent.click(screen.getByRole("button", { name: "Apply query options" })); await waitFor(() => expect(list).toHaveBeenLastCalledWith("articles", expect.objectContaining({ limit: 25, fields: "id,title", expand: "author", cursor: undefined }), expect.any(AbortSignal))); fireEvent.click(screen.getByRole("button", { name: "Next page" })); await waitFor(() => expect(list).toHaveBeenLastCalledWith("articles", expect.objectContaining({ cursor: "continuation", limit: 25, fields: "id,title", expand: "author" }), expect.any(AbortSignal)));
});
it("retains rich auth field validation when only registration is toggled", async () => {
  const h = scriptedAdmin("/admin/auth-collections"); const rich = { ...realm, fields: [{ ...realm.fields[0], required: true, hidden: true, options: { maxLength: 80 } }] };
  h.backend.authCollections.listAuthCollections = vi.fn(async () => [rich]); const update = vi.fn(async (_id, input) => ({ ...rich, ...input })); h.backend.authCollections.updateAuthCollection = update; renderAdmin(h);
  const toggle = await screen.findByLabelText("Registration enabled"); expect(screen.getByLabelText("members profile schema")).toHaveAttribute("readonly"); fireEvent.click(toggle);
  await waitFor(() => expect(update).toHaveBeenCalledWith(realm.id, expect.objectContaining({ registrationEnabled: true, fields: rich.fields, rules: rich.rules }), expect.any(AbortSignal)));
  expect(update.mock.calls[0][1]).not.toHaveProperty("recordId");
});
it("never issues a hidden auth collection read when the server disables that module", async () => {
  const { h, show } = recordsHarness(); h.backend.capabilities = { authCollections: false }; show(); await waitFor(() => expect(screen.queryByText("Collection users")).toBeNull()); expect(h.backend.authCollections.listAuthCollections).not.toHaveBeenCalled();
});
it("keeps supported child screens reachable when their parent module is absent", async () => {
  const h = scriptedAdmin(); h.backend.capabilities = { users: false, administrators: true, storage: false, backupStorage: true }; h.backend.administrators = { list: vi.fn(async () => ({ items: [] })), create: vi.fn(), setActive: vi.fn(), resetPassword: vi.fn(), revokeSessions: vi.fn() }; h.backend.schema.catalog = vi.fn(async () => new CollectionCatalog(1, [])); renderAdmin(h); await screen.findByRole("heading", { name: "Collections" }); const nav = screen.getByRole("navigation", { name: "Administration" }); expect(within(nav).getByRole("link", { name: "Administrators" })).toBeVisible(); expect(within(nav).getByRole("link", { name: "Backup storage" })).toBeVisible(); expect(within(nav).queryByRole("link", { name: "Users" })).toBeNull();
});
const settingsFixture: AdminSettings = { batch: { enabled: true, maxRequests: 10, maxBodyBytes: 4096, timeoutMs: 1000 }, logs: { retentionDays: 7, level: "info", includeIP: false, includeAuth: false }, proxy: { trustedProxyCIDRs: [] }, backups: { enabled: false, keep: 7, schedule: "@every 24h", timezone: "UTC" }, application: { theme: "system", accent: "", branding: "" }, rateLimits: { admin: { accountMaxFailures: 5, networkMaxFailures: 20, windowSeconds: 900, blockSeconds: 900 }, user: { accountMaxFailures: 5, networkMaxFailures: 20, registrationNetworkMaxFailures: 5, windowSeconds: 900, blockSeconds: 900 }, enabled: false, rules: [], excludedIPs: [] }, revision: 1 };
it("does not let an old token-policy response release a newly mounted form's draft", async () => {
  const h = scriptedAdmin(); const ledger = draftLedger(); let finish!: (value: { purpose: string; ttlSeconds: number; keyVersion: string; keyConfigured: boolean }) => void;
  h.backend.authCollections.tokenPolicies = vi.fn(async () => [{ purpose: "session", ttlSeconds: 3600, keyVersion: "v1", keyConfigured: true }]); h.backend.authCollections.putTokenPolicy = vi.fn(() => new Promise<import("./backend/ports").AuthTokenPolicy>(resolve => { finish = resolve; }));
  const view = () => render(<UnsavedDrafts ledger={ledger}><AuthTokenPolicies collectionId={realm.id} /></UnsavedDrafts>, { wrapper: sessionWrapper(h.backend, h.history) });
  const first = view(); await userEvent.click(screen.getByText("Token policies")); let group = await screen.findByRole("group", { name: "Session" }); fireEvent.change(within(group).getByLabelText(/Token lifetime/), { target: { value: "4000" } }); fireEvent.click(within(group).getByRole("button", { name: "Save token policy" })); first.unmount();
  view(); await userEvent.click(screen.getByText("Token policies")); group = await screen.findByRole("group", { name: "Session" }); fireEvent.change(within(group).getByLabelText(/Token lifetime/), { target: { value: "5000" } }); expect(ledger.held()).toBe(true);
  await act(async () => { finish({ purpose: "session", ttlSeconds: 4000, keyVersion: "v1", keyConfigured: true }); }); expect(within(group).getByLabelText(/Token lifetime/)).toHaveValue(5000); expect(ledger.held()).toBe(true);
});
it("abandons settings reload after unmount without reading again or applying an old theme", async () => {
  const h = scriptedAdmin(); let finish!: () => void; const get = vi.fn(async () => structuredClone(settingsFixture)); h.backend.settings = { get, save: vi.fn(), reload: vi.fn(() => new Promise<void>(resolve => { finish = resolve; })) };
  const view = render(<SettingsPage />, { wrapper: sessionWrapper(h.backend, h.history) }); await screen.findByLabelText("Branding"); fireEvent.click(screen.getByRole("button", { name: "Reload server settings" })); view.unmount(); document.documentElement.dataset.theme = "light"; await act(async () => { finish(); }); expect(get).toHaveBeenCalledTimes(1); expect(document.documentElement.dataset.theme).toBe("light");
});
it("saves batch and app-user limits together and reloads the server document without discarding a draft", async () => {
  const h = scriptedAdmin(); let current = structuredClone(settingsFixture); const save = vi.fn(async value => { current = { ...value, revision: 2 }; return current; }); const reload = vi.fn(async () => { current = { ...current, batch: { ...current.batch, maxRequests: 42 } }; }); h.backend.settings = { get: vi.fn(async () => current), save, reload };
  render(<SettingsPage />, { wrapper: sessionWrapper(h.backend, h.history) }); await screen.findByLabelText("Branding"); fireEvent.change(screen.getByLabelText("Maximum operations per batch"), { target: { value: "25" } }); fireEvent.change(screen.getByLabelText("Registration failures per network"), { target: { value: "9" } }); expect(screen.getByRole("button", { name: "Reload server settings" })).toBeDisabled(); fireEvent.click(screen.getByRole("button", { name: "Save settings" })); await screen.findByText("Settings saved."); expect(save).toHaveBeenCalledWith(expect.objectContaining({ batch: expect.objectContaining({ maxRequests: 25 }), rateLimits: expect.objectContaining({ user: expect.objectContaining({ registrationNetworkMaxFailures: 9 }) }) }), expect.any(AbortSignal)); fireEvent.click(screen.getByRole("button", { name: "Reload server settings" })); await screen.findByText("Server settings reloaded from storage."); expect(reload).toHaveBeenCalledWith(expect.any(AbortSignal)); expect(screen.getByLabelText("Maximum operations per batch")).toHaveValue(42);
});
it("protects an unsaved OIDC provider edit when navigating away", async () => {
  const h = scriptedAdmin("/admin/auth-collections"); h.backend.authCollections.listAuthCollections = vi.fn(async () => [realm]); h.backend.authCollections.getAdministration = vi.fn(async () => ({ policy: { passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 1024, passwordPattern: "", identityFields: ["email"], mfaEnabled: false, mfaDurationSeconds: 300, mfaRule: "", otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5 }, providers: [], templates: [] })); vi.spyOn(window, "confirm").mockReturnValue(false); renderAdmin(h); await userEvent.click(await screen.findByText("Authentication settings")); fireEvent.change(screen.getByLabelText("OIDC issuer"), { target: { value: "https://identity.example.test" } }); expect(screen.getByRole("button", { name: /^Reload$/ })).toBeDisabled(); fireEvent.click(screen.getByRole("button", { name: /^Reload$/ })); expect(h.backend.authCollections.listAuthCollections).toHaveBeenCalledTimes(1); fireEvent.click(screen.getByRole("link", { name: "Users" })); expect(h.history.path()).toBe("/admin/auth-collections"); expect(window.confirm).toHaveBeenCalledTimes(1); expect(screen.getByLabelText("OIDC issuer")).toHaveValue("https://identity.example.test");
});

it("keeps collection user CRUD reachable when only auth collections are supported", async () => {
  const { h, port } = recordsHarness(); h.history.replace("/admin/auth-collections"); h.backend.capabilities = { users: false, authCollections: true }; vi.mocked(port.list).mockResolvedValue([record]); renderAdmin(h); await screen.findByRole("heading", { name: "Collection users" }); fireEvent.change(await screen.findByLabelText("Authentication collection"), { target: { value: realm.id } }); expect(await screen.findByText(record.email!)).toBeVisible();
});
it("includes other auth realms and preserves existing relation targets in the schema editor", async () => {
  const h = scriptedAdmin(); h.backend.schema.catalog = vi.fn(async () => new CollectionCatalog(1, [])); const related = { ...realm, fields: [{ name: "manager", type: "relation" as const, required: false, indexed: false, unique: false, relation: { targetCollection: "legacy_staff", cardinality: "single" as const, minSelect: 0, maxSelect: 1, onDelete: "restrict" as const } }] };
  render(<AuthCollectionSchema collection={related} authCollections={[realm, { ...realm, id: "staff", name: "staff" }]} onSaved={vi.fn()} />, { wrapper: sessionWrapper(h.backend, h.history) }); await userEvent.click(screen.getByText("Fields, access rules and record identity")); const target = await screen.findByLabelText("Field 1 relation target"); await waitFor(() => expect(target).toBeEnabled()); expect(target).toHaveValue("legacy_staff"); expect(within(target).getByRole("option", { name: /^staff$/ })).toBeVisible();
});
it("disables the whole auth collection reload while a child token policy has an unsaved draft", async () => {
  const h = scriptedAdmin("/admin/auth-collections"); h.backend.authCollections.listAuthCollections = vi.fn(async () => [realm]); h.backend.authCollections.tokenPolicies = vi.fn(async () => [{ purpose: "session", ttlSeconds: 3600, keyVersion: "v1", keyConfigured: true }]); h.backend.authCollections.putTokenPolicy = vi.fn(); renderAdmin(h); await userEvent.click(await screen.findByText("Token policies")); const group = await screen.findByRole("group", { name: "Session" }); fireEvent.change(within(group).getByLabelText(/Token lifetime/), { target: { value: "5000" } }); expect(screen.getByRole("button", { name: /^Reload$/ })).toBeDisabled(); expect(within(group).getByLabelText(/Token lifetime/)).toHaveValue(5000);
});
