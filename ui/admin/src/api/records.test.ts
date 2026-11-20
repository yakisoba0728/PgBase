import { afterEach, expect, it, vi } from "vitest";
import * as http from "./http";
import { replaceRecordFiles, uploadRecordFile } from "./records";
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  http.onCsrfRejected(async () => null, () => null);
});
it("replaces a required attachment with one conditional atomic multipart PATCH", async () => {
  const upload = vi.spyOn(http, "upload").mockResolvedValue({ results: [{ body: { id: "CaseSensitive" }, etag: '"next"' }] });
  const file = new File(["new"], "new.txt");
  await replaceRecordFiles("notes", "CaseSensitive", '"before"', "cover", file, "shown-file-id");
  const [path, body] = upload.mock.calls[0];
  const form = body as FormData;
  expect(path).toBe("/api/v1/collections/batch");
  expect(JSON.parse(String(form.get("requests")))).toEqual({ requests: [{ method: "PATCH", url: "/api/v1/collections/notes/records/CaseSensitive", headers: { "If-Match": '"before"' }, body: { cover: { $file: "file_0", $replaces: "shown-file-id" } } }] });
  expect(form.get("file_0")).toBeInstanceOf(File);
});

it.each(["local session", "administrator identity"])("does not upload after the %s changes during file hashing", async (change) => {
  let administrator = "account-A";
  http.onCsrfRejected(async () => administrator, () => administrator);
  const upload = vi.spyOn(http, "upload").mockResolvedValue({ id: "unexpected" });
  let finishHash!: (bytes: ArrayBuffer) => void;
  let beganHash!: () => void;
  const entered = new Promise<void>((resolve) => { beganHash = resolve; });
  const hashing = new Promise<ArrayBuffer>((resolve) => { finishHash = resolve; });
  vi.stubGlobal("crypto", { subtle: { digest: () => { beganHash(); return hashing; } } });
  const file = new File(["attachment"], "file.txt");
  Object.defineProperty(file, "arrayBuffer", { value: async () => new ArrayBuffer(10) });
  const pending = uploadRecordFile("notes", "record-A", "attachment", file);
  await entered;
  if (change === "local session") http.advanceSession();
  else administrator = "account-B";
  finishHash(new ArrayBuffer(32));
  await expect(pending).rejects.toMatchObject({ code: "session_changed" });
  expect(upload).not.toHaveBeenCalled();
});

it("retains the upload file digest and metadata when its administrator stays current", async () => {
  const upload = vi.spyOn(http, "upload").mockResolvedValue({ id: "file" });
  vi.stubGlobal("crypto", { subtle: { digest: async () => new Uint8Array(32).fill(42).buffer } });
  const file = new File(["attachment"], "file.txt");
  Object.defineProperty(file, "arrayBuffer", { value: async () => new ArrayBuffer(10) });
  await expect(uploadRecordFile("notes", "record-A", "attachment", file)).resolves.toEqual({ id: "file" });
  const [path, body, options] = upload.mock.calls[0];
  expect(path).toBe("/api/admin/collections/notes/records/record-A/files/attachment");
  expect((body as FormData).get("file")).toBeInstanceOf(File);
  expect(options?.headers).toEqual({ "X-PgBase-File-Size": String(file.size), "X-PgBase-File-SHA256": "2a".repeat(32) });
});
