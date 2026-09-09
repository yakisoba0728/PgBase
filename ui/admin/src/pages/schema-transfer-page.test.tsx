import { act, fireEvent, screen } from "@testing-library/react";
import { vi } from "vitest";
import { renderAdmin, scriptedAdmin, administrator } from "../test/harness";
import type { SchemaDocument, SchemaImportPlan } from "../backend/ports";
import { schemaDocumentDigest } from "./schema-transfer-page";

const document: SchemaDocument = { format: "pgbase.schema/v1", collections: [{ name: "posts", fields: [], indexes: [], userAccess: false, rules: {} }] };
const plan: SchemaImportPlan = { document, changes: [{ operation: "create", baseVersion: 1, definition: { name: "posts", tableName: "posts", fields: [] }, sql: [] }], baseVersion: 1, checksum: "schema-checksum", policyChecksum: "policy-checksum", operationId: "operation-1", idempotencyKey: "retry-1" };

function harnessWithSchema() {
  const harness = scriptedAdmin("/admin/schema-transfer");
  Object.assign(harness.backend.schema, {
    exportDocument: vi.fn(async () => document),
    previewImport: vi.fn(async () => plan),
    applyImport: vi.fn(async () => ({ operationId: "operation-1", status: "unknown", version: 1, applied: 0 })),
    importStatus: vi.fn(async () => ({ operationId: "operation-1", status: "running", version: 1, applied: 0 })),
  });
  return harness;
}

describe("schema transfer recovery", () => {
  beforeEach(() => sessionStorage.clear());

  it("retains bounded recovery metadata for unknown results and retries the exact in-memory plan", async () => {
    const harness = harnessWithSchema(); renderAdmin(harness);
    fireEvent.change(await screen.findByLabelText("Schema document"), { target: { value: JSON.stringify(document) } });
    fireEvent.click(screen.getByRole("button", { name: "Preview import" }));
    await screen.findByText("1 schema changes from version 1.");
    fireEvent.click(screen.getByRole("button", { name: "Apply import" }));
    await screen.findByText(/cannot confirm operation operation-1/i);
    const stored = sessionStorage.getItem(`pgbase.schemaImportOperation.v1.${administrator.id}`) ?? "";
    expect(stored).toContain("operation-1"); expect(stored).not.toContain("posts");
    fireEvent.click(screen.getByRole("button", { name: "Retry exact import" }));
    await act(async () => {});
    expect(harness.backend.schema.applyImport).toHaveBeenCalledTimes(2);
    expect(harness.backend.schema.applyImport).toHaveBeenLastCalledWith(plan);
    expect(screen.getByRole("button", { name: "Preview import" })).toBeDisabled();
  });

  it("recovers committed status after reload and deletes terminal recovery data", async () => {
    const digest = await schemaDocumentDigest(document);
    sessionStorage.setItem(`pgbase.schemaImportOperation.v1.${administrator.id}`, JSON.stringify({ version: 1, administratorId: administrator.id, operationId: "operation-2", idempotencyKey: "retry-2", baseVersion: 1, checksum: "schema-checksum", policyChecksum: "policy-checksum", documentDigest: digest }));
    const harness = harnessWithSchema();
    Object.assign(harness.backend.schema, { importStatus: vi.fn(async () => ({ operationId: "operation-2", status: "committed", version: 2, applied: 1 })) });
    renderAdmin(harness);
    await screen.findByText(/committed at version 2 with 1 changes/i);
    expect(sessionStorage.getItem(`pgbase.schemaImportOperation.v1.${administrator.id}`)).toBeNull();
  });

  it("ignores a late preview after the document changes", async () => {
    let settle!: (value: SchemaImportPlan) => void;
    const pending = new Promise<SchemaImportPlan>((resolve) => { settle = resolve; });
    const harness = harnessWithSchema(); Object.assign(harness.backend.schema, { previewImport: vi.fn(() => pending) }); renderAdmin(harness);
    const editor = await screen.findByLabelText("Schema document");
    fireEvent.change(editor, { target: { value: JSON.stringify(document) } }); fireEvent.click(screen.getByRole("button", { name: "Preview import" }));
    fireEvent.change(editor, { target: { value: JSON.stringify({ ...document, collections: [] }) } });
    await act(async () => settle(plan));
    expect(screen.queryByLabelText("Import preview")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Preview import" })).not.toBeDisabled();
  });

  it("does not send an import when recovery storage fails and releases busy state", async () => {
    const harness = harnessWithSchema(); renderAdmin(harness);
    fireEvent.change(await screen.findByLabelText("Schema document"), { target: { value: JSON.stringify(document) } });
    fireEvent.click(screen.getByRole("button", { name: "Preview import" })); await screen.findByLabelText("Import preview");
    const storage = vi.spyOn(Storage.prototype, "setItem").mockImplementationOnce(() => { throw new DOMException("quota"); });
    fireEvent.click(screen.getByRole("button", { name: "Apply import" }));
    await screen.findByText(/recovery information.*not sent/i);
    expect(harness.backend.schema.applyImport).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Apply import" })).not.toBeDisabled();
    storage.mockRestore();
    const digest = vi.spyOn(crypto.subtle, "digest").mockRejectedValueOnce(new DOMException("crypto unavailable"));
    fireEvent.click(screen.getByRole("button", { name: "Apply import" }));
    await screen.findByText(/recovery information.*not sent/i);
    expect(harness.backend.schema.applyImport).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Apply import" })).not.toBeDisabled();
    digest.mockRestore();
  });

  it("invalidates an old plan when export replaces the editor and shows no-op document details", async () => {
    const harness = harnessWithSchema();
    Object.assign(harness.backend.schema, { previewImport: vi.fn(async () => ({ ...plan, changes: [] })) });
    renderAdmin(harness);
    fireEvent.change(await screen.findByLabelText("Schema document"), { target: { value: JSON.stringify(document) } });
    fireEvent.click(screen.getByRole("button", { name: "Preview import" }));
    expect(await screen.findByText(/posts/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Export schema" }));
    await act(async () => {});
    expect(screen.queryByLabelText("Import preview")).not.toBeInTheDocument();
  });
});
