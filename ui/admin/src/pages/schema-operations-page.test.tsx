import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { OperationReceipt } from "../backend/ports";
import { renderAdmin, scriptedAdmin } from "../test/harness";

function receipt(operationId: string, patch: Partial<OperationReceipt> = {}): OperationReceipt {
  return {
    operationId, operation: "collection_create", collectionId: "posts-id", status: "committed",
    baseVersion: 7, committedVersion: 8, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:01Z", ...patch,
  };
}

const rowOrder = () => screen.getAllByRole("listitem").map((row) => row.querySelector(".operation-id")?.textContent);

describe("schema operations page", () => {
  it("renders one bounded page of redacted receipts", async () => {
    const harness = scriptedAdmin("/admin/schema-operations");
    harness.script.operations.push({
      value: {
        items: [receipt("op-1", { status: "failed", failureCode: "apply_failed" })],
      },
    });
    renderAdmin(harness);

    expect(await screen.findByText("Not applied")).toBeVisible();
    expect(screen.getByRole("heading", { name: "Schema operations" })).toBeVisible();
    // One collection has many operations, so the operation's own identity is
    // what names a row — and what an administrator quotes when asking about it.
    expect(rowOrder()).toEqual(["op-1"]);
    expect(screen.getByText("The change did not apply and nothing was left behind.")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Next page" })).not.toBeInTheDocument();
  });

  it("keeps the ledger's ordering when the next page is appended", async () => {
    const harness = scriptedAdmin("/admin/schema-operations");
    // One shared creation instant, so only the ledger's own tie-break orders
    // these rows: any client-side sort would reshuffle them.
    harness.script.operations.push({ value: { items: [receipt("op-d"), receipt("op-b")], nextCursor: "page-2" } });
    harness.script.operations.push({ value: { items: [receipt("op-c"), receipt("op-a")] } });
    renderAdmin(harness);

    expect(await screen.findByText("op-d")).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findByText("op-a");

    expect(rowOrder()).toEqual(["op-d", "op-b", "op-c", "op-a"]);
  });

  it("reports a cursor another owner cannot reuse without discarding the history already read", async () => {
    const harness = scriptedAdmin("/admin/schema-operations");
    harness.script.operations.push({ value: { items: [receipt("op-1")], nextCursor: "another-owners-cursor" } });
    harness.script.operations.push({ fault: { kind: "invalid-cursor" } });
    renderAdmin(harness);

    expect(await screen.findByText("op-1")).toBeVisible();
    await userEvent.click(screen.getByRole("button", { name: "Next page" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("This history page is no longer available.");
    expect(screen.getByText("op-1")).toBeVisible();
    expect(screen.queryByRole("button", { name: "Next page" })).not.toBeInTheDocument();
  });

  it.each(["running", "unknown"] as const)("says a %s operation is undecided rather than applied or failed", async (status) => {
    const harness = scriptedAdmin("/admin/schema-operations");
    harness.script.operations.push({ value: { items: [receipt("op-1", { status, committedVersion: undefined })] } });
    renderAdmin(harness);

    expect(await screen.findByText(/has not been decided yet/)).toBeVisible();
    expect(screen.queryByText("Applied")).not.toBeInTheDocument();
    expect(screen.queryByText("Not applied")).not.toBeInTheDocument();
  });

  it("reports an empty ledger as empty rather than as a failure", async () => {
    const harness = scriptedAdmin("/admin/schema-operations");
    harness.script.operations.push({ value: { items: [] } });
    renderAdmin(harness);

    expect(await screen.findByText("No schema operations yet.")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
