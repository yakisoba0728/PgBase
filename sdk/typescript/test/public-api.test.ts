import type { AuthMethods, AuthRealm, BatchOperation, BatchOptions, Query, Records, RecordWriteOptions } from "../src/index.js";

type Note = { id: string; title: string; body?: string };

const query: Query = { fields: "id,title", expand: "author" };
const operations: BatchOperation<Note>[] = [
  { operation: "upsert", collection: "notes", id: "00000000-0000-4000-8000-000000000099", input: { title: "upserted" } },
];

declare const records: Records<Note>;
declare const realm: AuthRealm;
declare const methods: AuthMethods;
void records.list(query);
void records.get("note-1", { fields: "id,title" });
void records.get("note-1", new AbortController().signal);
const writeOptions: RecordWriteOptions = { fields: "id,title" };
void records.create({ title: "created" }, "create-note-0001", writeOptions);
void records.update("note-1", { title: "updated" }, '"pgb1.current"', writeOptions);
void realm.methods();
void realm.profile({ fields: "id,email" });
void realm.updateProfile({ title: "person" }, { fields: "id,title" });
void realm.refresh();
void methods.oauthProviders;
void operations;
const batchOptions: BatchOptions = { fields: "id,title" };
void batchOptions;
