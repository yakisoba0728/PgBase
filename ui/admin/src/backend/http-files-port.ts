import { RecordError, type AdminBackend, type FileInfo } from "./ports";
import { decode, type Request } from "./http-transport";

export interface FilesTransport {
  recordCall(request: Request): Promise<{ data: unknown }>;
  fileCall(path: string, request: Omit<Request, "path">): Promise<unknown>;
  csrfToken(): string;
}

const fieldPath = (collection: string, recordID: string, field: string) =>
  `/api/admin/collections/${encodeURIComponent(collection)}/records/${encodeURIComponent(recordID)}/files/${encodeURIComponent(field)}`;
const filePath = (id: string) => `/api/admin/files/${encodeURIComponent(id)}`;

/** File endpoint codecs share the record transport, including its error mapping. */
export function createFilesPort(transport: FilesTransport): AdminBackend["files"] {
  return {
    listFiles: async (collection, recordID, field, signal): Promise<FileInfo[]> =>
      ((await transport.recordCall({ path: fieldPath(collection, recordID, field), signal })).data as { items: FileInfo[] }).items,
    uploadFile: async (collection, recordID, field, file, signal): Promise<FileInfo> => {
      const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", await file.arrayBuffer()));
      const body = new FormData();
      body.append("file", file, file.name);
      return transport.fileCall(fieldPath(collection, recordID, field), {
        method: "POST", body,
        headers: {
          "X-PgBase-CSRF": transport.csrfToken(),
          "X-PgBase-File-Size": String(file.size),
          "X-PgBase-File-SHA256": Array.from(digest, (byte) => byte.toString(16).padStart(2, "0")).join(""),
        }, signal,
      }) as Promise<FileInfo>;
    },
    deleteFile: async (id, signal): Promise<void> => {
      await transport.fileCall(filePath(id), { method: "DELETE", headers: { "X-PgBase-CSRF": transport.csrfToken() }, signal });
    },
    createDownloadToken: async (id, variant = "original", signal): Promise<string> =>
      ((await transport.fileCall(`${filePath(id)}/token`, { method: "POST", body: { variant }, csrf: true, signal })) as { token: string }).token,
    downloadBlob: async (id, token, variant = "original", signal): Promise<Blob> => {
      const path = new URL(filePath(id), window.location.origin);
      path.searchParams.set("token", token);
      if (variant !== "original") path.searchParams.set("variant", variant);
      const response = await fetch(path, { credentials: "include", signal });
      if (!response.ok) throw new RecordError(response.status === 404 ? "not-found" : "failed");
      return response.blob();
    },
  };
}

export async function callFile(
  recordCall: (request: Request) => Promise<{ data: unknown }>,
  path: string,
  request: Omit<Request, "path">,
): Promise<unknown> {
  if (!(request.body instanceof FormData)) return (await recordCall({ ...request, path })).data;
  const response = await fetch(path, {
    method: request.method, headers: request.headers, body: request.body,
    credentials: "include", signal: request.signal,
  });
  if (!response.ok) throw new RecordError(response.status === 404 ? "not-found" : "failed");
  return response.status === 204 ? undefined : decode(response);
}
