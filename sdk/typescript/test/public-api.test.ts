import type { AuthMethods, AuthRealm, BatchOperation, BatchOptions, CollectionChange, CollectionDefinition, CollectionScaffolds, OAuthLaunchOptions, PocketBaseListOptions, PocketBasePage, Query, RecordIDPolicy, RecordJSONResult, RecordModifiers, RealtimeEvent, Records, RecordWriteOptions, SchemaDocument, SchemaDocumentAuthCollection, SchemaDocumentAuthPolicy, SchemaDocumentCollection, SchemaPlan } from "../src/index.js";
import { AuthStore, browserAuthStorage } from "../dist/index.js";
import type { AuthStateStorage, PgBaseClient } from "../dist/index.js";
import type { AuthCreationOptions, RealtimeStatus } from "../dist/index.js";

declare const stateStorage: AuthStateStorage;
declare const client: PgBaseClient;
new AuthStore(stateStorage);
new AuthStore(browserAuthStorage(localStorage));
client.autoCancellation = false;
void client.records("notes").get("one", { requestKey: "editor" });
void client.records("notes").listJSON({ requestKey: null });
const signup: AuthCreationOptions<{ displayName: string; newsletter: boolean }> = { profile: { displayName: "Ada", newsletter: false }, fields: "id,email" };
void client.register("a@test", "password", signup);
void client.realm("members").register("a@test", "password", signup);
void client.realm("members").oauthComplete("github", "state", "code", signup);
// @ts-expect-error A typed profile preserves its declared value types.
const badSignup: AuthCreationOptions<{ newsletter: boolean }> = { profile: { newsletter: "yes" } };
// @ts-expect-error Login is not a profile creation/update operation.
void client.login("a@test", "password", { profile: {} });
void badSignup;
const unobserve = client.realtime.onStatus((status: RealtimeStatus) => { void status.state; void status.error; void status.lastEventId; });
unobserve();
void client.realtime.resetCursor();

type Note = { id: string; title: string; body?: string };

const query: Query = { fields: "id,title", expand: "author" };
const operations: BatchOperation<Note>[] = [
  { operation: "upsert", collection: "notes", id: "00000000-0000-4000-8000-000000000099", input: { title: "upserted" } },
];

declare const records: Records<Note>;
declare const realm: AuthRealm;
declare const methods: AuthMethods;
void records.list(query);
const pocketBaseOptions: PocketBaseListOptions = { sort: "@random", skipTotal: true };
void records.getList(2, 25, pocketBaseOptions);
declare const pocketBasePage: PocketBasePage<Note>;
void pocketBasePage.totalItems;
const modifiers: RecordModifiers = { "score+": 1, "+tags": ["urgent"], "tags-": ["old"] };
void records.update("note-1", modifiers, '"pgb1.current"');
void records.get("note-1", { fields: "id,title" });
void records.get("note-1", new AbortController().signal);
const writeOptions: RecordWriteOptions = { fields: "id,title" };
void records.create({ title: "created" }, "create-note-0001", writeOptions);
void records.create({ id: "CaseSensitive01", title: "created" }, "create-text-note-0001");
// @ts-expect-error Public text identities must be strings.
void records.create({ id: 123, title: "created" }, "create-invalid-note-0001");
const textIDBatch: BatchOperation<Note> = { operation: "create", collection: "notes", input: { id: "CaseSensitive01", title: "created" } };
void textIDBatch;
void records.update("note-1", { title: "updated" }, '"pgb1.current"', writeOptions);

const rawPage: Promise<string> = records.listJSON(query);
const rawGet: Promise<RecordJSONResult> = records.getJSON("CaseSensitive01", { fields: "id,title" });
const rawGetSignal: Promise<RecordJSONResult> = records.getJSON("CaseSensitive01", new AbortController().signal);
const rawCreate: Promise<RecordJSONResult> = records.createJSON('{"title":"created"}', "create-json-note-0001", writeOptions);
const rawUpdate: Promise<RecordJSONResult> = records.updateJSON("CaseSensitive01", '{"title":"updated"}', '"pgb1.current"', writeOptions);
void [rawPage, rawGet, rawGetSignal, rawCreate, rawUpdate];
// @ts-expect-error Raw record methods accept JSON text instead of already rounded objects.
void records.createJSON({ title: "created" }, "create-invalid-json-0001");

const defaultIDPolicy: RecordIDPolicy = { kind: "uuid" };
const defaultTextIDPolicy: RecordIDPolicy = { kind: "text" };
const textIDPolicy: RecordIDPolicy = { kind: "text", minLength: 1, maxLength: 32, pattern: "^[A-Za-z0-9]+$" };
const generatedTextIDPolicy: RecordIDPolicy = { kind: "text", minLength: 10, maxLength: 10, pattern: "^[a-z]+$", autogeneratePattern: "[a-z]{10}" };
// @ts-expect-error UUID policies cannot specify text validation options.
const invalidUUIDPolicy: RecordIDPolicy = { kind: "uuid", minLength: 1, maxLength: 32, pattern: ".*" };
// @ts-expect-error A custom text policy requires lengths and a validation pattern.
const incompleteTextPolicy: RecordIDPolicy = { kind: "text", minLength: 1 };
const createCollection: CollectionChange = { operation: "create", name: "notes", fields: [{ name: "title", type: "text" }], recordId: textIDPolicy };
const updateCollection: CollectionChange = { operation: "update", name: "notes", fields: [], recordId: textIDPolicy };
const updateView: CollectionChange = { operation: "update", name: "note_titles", view: { source: "notes", projection: ["title"], filter: { op: "eq", field: "title", value: "visible" } } };
const documentCollection: SchemaDocumentCollection = { name: "notes", recordId: textIDPolicy, userAccess: false, rules: {} };
const authPolicy: SchemaDocumentAuthPolicy = {
  passwordEnabled: true, minimumPasswordBytes: 12, maximumPasswordBytes: 128, passwordPattern: "",
  identityFields: ["email"], mfaEnabled: false, mfaDurationSeconds: 300, mfaRule: "",
  otpEnabled: false, otpDurationSeconds: 300, otpMaxAttempts: 5,
};
const authDocumentCollection: SchemaDocumentAuthCollection = {
  name: "members", registrationEnabled: true, recordId: textIDPolicy, policy: authPolicy,
  rules: { list: null, view: "@request.auth.id = id", create: "", auth: "" }, tokenTTLs: { session: 3600 },
};
const authDocument: SchemaDocument = {
  format: "pgbase.schema/v1",
  collections: [{ name: "members", type: "auth", recordId: textIDPolicy, userAccess: false, rules: {} }],
  authCollections: [authDocumentCollection],
};
const legacyDocument: SchemaDocument = { format: "pgbase.schema/v1", collections: [documentCollection] };
declare const scaffolds: CollectionScaffolds;
const scaffoldDocument: SchemaDocument = {
  format: "pgbase.schema/v1", collections: [scaffolds.auth.collection], authCollections: [scaffolds.auth.authCollection],
};
void scaffoldDocument;
// @ts-expect-error Portable auth rules contain nullable source strings, not record rule objects.
const invalidAuthDocument: SchemaDocumentAuthCollection = { ...authDocumentCollection, rules: { view: { op: "owner" } } };
void [authDocument, legacyDocument, invalidAuthDocument];
declare const collection: CollectionDefinition;
declare const plan: SchemaPlan;
const catalogIDPolicy: RecordIDPolicy | undefined = collection.recordId;
const plannedIDPolicy: RecordIDPolicy | undefined = plan.definition.recordId;
void [defaultIDPolicy, defaultTextIDPolicy, generatedTextIDPolicy, invalidUUIDPolicy, incompleteTextPolicy, createCollection, updateCollection, updateView, documentCollection, catalogIDPolicy, plannedIDPolicy];
void realm.methods();
void realm.profile({ fields: "id,email" });
void realm.updateProfile({ title: "person" }, { fields: "id,title" });
void realm.refresh();
void methods.oauthProviders;
void methods.oauth2.enabled;
void operations;
const batchOptions: BatchOptions = { fields: "id,title" };
void batchOptions;
const launchOptions: OAuthLaunchOptions = { open: () => {} };
void realm.oauthStartAndOpen("github", "https://app.test/callback", launchOptions);
void records.events({ fields: "id,title" });
declare const realtimeEvent: RealtimeEvent<Note>;
void realtimeEvent.action;
