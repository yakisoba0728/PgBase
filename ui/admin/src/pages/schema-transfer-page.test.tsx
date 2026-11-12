import { act, fireEvent, screen, waitFor } from "@testing-library/react";
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
    await waitFor(() => expect(harness.backend.schema.applyImport).toHaveBeenCalledTimes(2));
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

  it("exports without replacing an edited import document or its preview", async () => {
    const harness = harnessWithSchema();
    Object.assign(harness.backend.schema, { previewImport: vi.fn(async () => ({ ...plan, changes: [] })) });
    renderAdmin(harness);
    fireEvent.change(await screen.findByLabelText("Schema document"), { target: { value: JSON.stringify(document) } });
    fireEvent.click(screen.getByRole("button", { name: "Preview import" }));
    expect(await screen.findByText(/posts/)).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Export schema" }));
    await act(async () => {});
    expect(screen.getByLabelText("Import preview")).toBeInTheDocument();
    expect(screen.getByLabelText("Schema document")).toHaveValue(JSON.stringify(document));
  });
});


it("reads a chosen JSON file into the import draft without sending a write", async () => {
  const harness = harnessWithSchema(); renderAdmin(harness);
  const file = new File([JSON.stringify(document)], "collections.json", { type: "application/json" });
  Object.assign(file, { text: async () => JSON.stringify(document) });
  fireEvent.change(await screen.findByLabelText("Import JSON file"), { target: { files: [file] } });
  await waitFor(() => expect(screen.getByLabelText("Schema document")).toHaveValue(JSON.stringify(document)));
  expect(harness.backend.schema.previewImport).not.toHaveBeenCalled(); expect(harness.backend.schema.applyImport).not.toHaveBeenCalled();
});

it("keeps schema transfer usable when session storage is denied", async () => {
  const remove = vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new DOMException("blocked", "SecurityError"); });
  try { const harness = harnessWithSchema(); renderAdmin(harness); expect(await screen.findByLabelText("Schema document")).toBeEnabled(); }
  finally { remove.mockRestore(); }
});


it("keeps a replacement import draft protected when an older import status becomes committed", async () => {
  sessionStorage.clear();
  const harness = harnessWithSchema();
  Object.assign(harness.backend.schema, { importStatus: vi.fn(async () => ({ operationId: "operation-1", status: "committed", version: 2, applied: 1 })) });
  renderAdmin(harness);
  const editor = await screen.findByLabelText("Schema document");
  fireEvent.change(editor, { target: { value: JSON.stringify(document) } });
  fireEvent.click(screen.getByRole("button", { name: "Preview import" }));
  await screen.findByLabelText("Import preview"); fireEvent.click(screen.getByRole("button", { name: "Apply import" }));
  await screen.findByText(/cannot confirm operation operation-1/i);
  const replacement = JSON.stringify({ ...document, collections: [] });
  fireEvent.change(editor, { target: { value: replacement } });
  fireEvent.click(screen.getByRole("button", { name: "Check import status" }));
  await screen.findByText(/committed at version 2/i);
  expect(editor).toHaveValue(replacement);
  const leaving = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(leaving);
  expect(leaving.defaultPrevented).toBe(true);
});

it("releases the imported draft guard only after its own apply response confirms commitment", async () => {
  sessionStorage.clear(); const harness = harnessWithSchema();
  Object.assign(harness.backend.schema, { applyImport: vi.fn(async () => ({ operationId: "operation-1", status: "committed", version: 2, applied: 1 })) });
  renderAdmin(harness);
  fireEvent.change(await screen.findByLabelText("Schema document"), { target: { value: JSON.stringify(document) } });
  fireEvent.click(screen.getByRole("button", { name: "Preview import" }));
  await screen.findByLabelText("Import preview"); fireEvent.click(screen.getByRole("button", { name: "Apply import" }));
  await screen.findByText(/committed at version 2/i);
  const leaving = new Event("beforeunload", { cancelable: true }); window.dispatchEvent(leaving);
  expect(leaving.defaultPrevented).toBe(false);
});
