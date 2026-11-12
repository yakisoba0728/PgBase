import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import type { BackupStorageSettings, FileStorageSettings } from "../api/storage";
import { backupUpdate, describePhase, draftFrom, fileUpdate, formatBytes, isDirty, storageErrorKey, validateDraft } from "./model";

const files: FileStorageSettings = { driver: "s3", bucket: "b", region: "us-east-1", endpoint: "", accessKey: "AK", usePathStyle: false, secretConfigured: true, revision: 3 };
const backups: BackupStorageSettings = { ...files, driver: "local", bucket: "", region: "", accessKey: "", secretConfigured: false, prefix: "", retention: 7, revision: 2 };

describe("describePhase", () => {
  it("reads object copying as a ratio", () => {
    expect(describePhase("copying_objects_3_of_12")).toMatchObject({ key: "storage.phase.copying_objects", vars: { done: 3, total: 12 }, ratio: 0.25 });
  });
  it("maps known phases and keeps unknown ones raw", () => {
    expect(describePhase("dumping_database").key).toBe("storage.phase.dumping_database");
    expect(describePhase("something_new")).toEqual({ raw: "something_new" });
  });
});

describe("storage drafts", () => {
  it("keeps the stored secret unless replaced or cleared", () => {
    const draft = draftFrom(files);
    expect(isDirty(draft, files)).toBe(false);
    expect(fileUpdate(draft, 3)).toEqual({ driver: "s3", bucket: "b", region: "us-east-1", endpoint: "", accessKey: "AK", secretKey: "", clearSecret: false, usePathStyle: false, revision: 3 });
    expect(fileUpdate({ ...draft, secretKey: "new", clearSecret: true }, 3)).toMatchObject({ secretKey: "", clearSecret: true });
  });

  it("sends only accepted backup fields and blanks S3 fields for local", () => {
    const update = backupUpdate({ ...draftFrom(backups), bucket: "left-over", retention: " 5 " }, 2);
    expect(Object.keys(update).sort()).toEqual(["accessKey", "bucket", "clearSecret", "driver", "endpoint", "prefix", "region", "retention", "revision", "secretKey", "usePathStyle"]);
    expect(update).toMatchObject({ driver: "local", bucket: "", retention: 5 });
  });

  it("mirrors the server's S3 checks per field", () => {
    const draft = { ...draftFrom(files), bucket: "", endpoint: "http://minio.example:9000" };
    expect(validateDraft(draft, true, false)).toEqual({ bucket: "storage.invalid.required", endpoint: "storage.invalid.endpoint" });
    expect(validateDraft({ ...draftFrom(files), endpoint: "http://127.0.0.1:9000" }, true, false)).toEqual({});
    expect(validateDraft({ ...draftFrom(files), clearSecret: true }, true, false)).toEqual({ secretKey: "storage.invalid.secretMissing" });
    expect(validateDraft({ ...draftFrom(files), accessKey: "" }, true, false)).toEqual({ accessKey: "storage.invalid.accessMissing" });
    expect(validateDraft({ ...draftFrom(backups), retention: "-1" }, false, true)).toEqual({ retention: "storage.invalid.retention" });
  });
});

it("formats sizes and maps storage errors", () => {
  expect(formatBytes(512)).toBe("512 B");
  expect(formatBytes(44817)).toBe("43.8 KB");
  expect(storageErrorKey(new ApiError(409, "storage_conflict", ""), "backups")).toBe("storage.error.conflict.backups");
  expect(storageErrorKey(new ApiError(503, "backup_storage_unavailable", ""), "backups")).toBe("storage.error.unavailable");
});
