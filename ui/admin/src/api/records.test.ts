import { afterEach, expect, it, vi } from "vitest";
import * as http from "./http";
import { replaceRecordFiles } from "./records";
afterEach(() => vi.restoreAllMocks());
it("replaces a required attachment with one conditional atomic multipart PATCH", async () => {
  const upload = vi.spyOn(http, "uploadForm").mockResolvedValue({ results: [{ body: { id: "CaseSensitive" }, etag: '"next"' }] });
  const file = new File(["new"], "new.txt");
  await replaceRecordFiles("notes", "CaseSensitive", '"before"', "cover", [file]);
  const [path, form] = upload.mock.calls[0];
  expect(path).toBe("/api/v1/collections/batch");
  expect(JSON.parse(String(form.get("requests")))).toEqual({ requests: [{ method: "PATCH", url: "/api/v1/collections/notes/records/CaseSensitive", headers: { "If-Match": '"before"' }, body: { cover: { $file: "file_0" } } }] });
  expect(form.get("file_0")).toBeInstanceOf(File);
});
