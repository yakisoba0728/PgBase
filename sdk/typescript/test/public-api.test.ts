import type { BatchOperation, Query, Records } from "../src/index.js";

type Note = { id: string; title: string; body?: string };

const query: Query = { fields: "id,title", expand: "author" };
const operations: BatchOperation<Note>[] = [
  { operation: "upsert", collection: "notes", id: "00000000-0000-4000-8000-000000000099", input: { title: "upserted" } },
];

declare const records: Records<Note>;
void records.list(query);
void records.get("note-1", { fields: "id,title" });
void records.get("note-1", new AbortController().signal);
void operations;
