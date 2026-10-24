import type {
  AdminBackend,
  CreateAttempt,
  ListOptions,
  RecordData,
  RecordPage,
  VersionedRecord,
} from "./ports";

type Request = {
  path: string;
  method?: string;
  body?: unknown;
  headers?: Record<string, string>;
  signal?: AbortSignal;
  csrf?: boolean;
};
type Reply = { data: unknown; headers: Headers; status: number };

export interface RecordsTransport {
  recordCall(request: Request): Promise<Reply>;
  versioned(collection: string, id: string, reply: Reply): VersionedRecord;
  multipartRecord(
    collection: string,
    id: string | undefined,
    input: RecordData,
    files: Record<string, File[]>,
    headers: Record<string, string>,
  ): Promise<VersionedRecord>;
}

const recordPath = (collection: string, id?: string) => {
  const base = `/api/v1/collections/${encodeURIComponent(collection)}/records`;
  return id === undefined ? base : `${base}/${encodeURIComponent(id)}`;
};

const fileIdentity = (files: Record<string, File[]>) =>
  Object.fromEntries(
    Object.entries(files).map(([field, selected]) => [
      field,
      selected.map(
        (file) => `${file.name}:${file.size}:${file.type}:${file.lastModified}`,
      ),
    ]),
  );

/** Endpoint codecs for the record port; auth, CSRF, and response handling stay in RecordsTransport. */
export function createRecordsPort(
  transport: RecordsTransport,
): AdminBackend["records"] {
  return {
    list: async (
      collection: string,
      options: ListOptions,
      signal?: AbortSignal,
    ): Promise<RecordPage> => {
      const query = new URLSearchParams({ limit: String(options.limit ?? 50) });
      if (options.fields) query.set("fields", options.fields);
      if (options.expand) query.set("expand", options.expand);
      if (options.cursor) query.set("cursor", options.cursor);
      if (options.filter) query.set("filter", options.filter);
      if (options.sort) query.set("sort", options.sort);
      return (
        await transport.recordCall({
          path: `${recordPath(collection)}?${query}`,
          signal,
        })
      ).data as RecordPage;
    },
    get: async (collection, id, signal) =>
      transport.versioned(
        collection,
        id,
        await transport.recordCall({
          path: recordPath(collection, id),
          signal,
        }),
      ),
    create: async (attempt: CreateAttempt, input: RecordData) => {
      const reply = await transport.recordCall({
        path: recordPath(attempt.collection),
        method: "POST",
        body: input,
        csrf: true,
        headers: { "Idempotency-Key": attempt.keyFor(input) },
      });
      return transport.versioned(
        attempt.collection,
        String((reply.data as RecordData).id ?? ""),
        reply,
      );
    },
    createWithFiles: (attempt, input, files) =>
      transport.multipartRecord(attempt.collection, undefined, input, files, {
        "Idempotency-Key": attempt.keyFor({
          ...input,
          $files: fileIdentity(files),
        }),
      }),
    update: async (record, patch) =>
      transport.versioned(
        record.collection,
        record.id,
        await transport.recordCall({
          path: recordPath(record.collection, record.id),
          method: "PATCH",
          body: patch,
          csrf: true,
          headers: { "If-Match": record.precondition() },
        }),
      ),
    updateWithFiles: (record, patch, files) =>
      transport.multipartRecord(record.collection, record.id, patch, files, {
        "If-Match": record.precondition(),
      }),
    remove: async (record) => {
      await transport.recordCall({
        path: recordPath(record.collection, record.id),
        method: "DELETE",
        csrf: true,
        headers: { "If-Match": record.precondition() },
      });
    },
  };
}
