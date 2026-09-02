import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { scriptedHealth, scriptedStatus } from "../test/backend-script";
import { renderAdmin, scriptedAdmin } from "../test/harness";

describe("system page", () => {
  it("renders the operational status the backend reports and nothing else", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({
      value: {
        ...scriptedStatus({ migrationCurrent: 4, migrationLatest: 5, migrationDirty: true, catalogVersion: 9 }),
        // A redacted field the SystemPort type does not carry: it must not reach the page.
        internalDatabaseURL: "postgres://secret",
      } as never,
    });
    harness.script.health.push({ value: scriptedHealth() });
    renderAdmin(harness);

    expect(await screen.findByText("v1.2.3")).toBeVisible();
    expect(screen.getByRole("heading", { name: "System" })).toBeVisible();
    expect(screen.getByText("Reachable")).toBeVisible();
    expect(screen.getByText("4 / 5")).toBeVisible();
    expect(screen.getByText("Dirty")).toBeVisible();
    expect(screen.getByText("9")).toBeVisible();
    expect(screen.getByText("Not ready")).toBeVisible();
    expect(screen.queryByText(/postgres:\/\/secret/)).not.toBeInTheDocument();
  });

  it("reports a status it cannot read instead of loading forever", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({ fault: { kind: "unreachable" } });
    harness.script.health.push({ fault: { kind: "unreachable" } });
    renderAdmin(harness);

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load system status.");
    expect(screen.queryByText("Loading system status…")).not.toBeInTheDocument();
  });

  // The reading a probe could not confirm is the one an operator must not read
  // as a fact: a catalog version of 0 is a real value on a fresh instance and a
  // placeholder on an unreachable one, and only the probe tells them apart.
  it("shows an unconfirmed reading as unavailable rather than as its value", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({
      value: scriptedStatus({
        databaseReachable: false, migrationCurrent: 0, migrationLatest: 0, catalogVersion: 0,
        unavailable: [
          { component: "database", code: "unavailable" },
          { component: "migration", code: "timeout" },
          { component: "catalog", code: "not_sampled" },
        ],
      }),
    });
    harness.script.health.push({ value: scriptedHealth() });
    renderAdmin(harness);

    await screen.findByText("v1.2.3");
    expect(screen.queryByText("0 / 0")).not.toBeInTheDocument();
    // The database probe and both readings the migration probe covers.
    expect(screen.getAllByText("Unavailable")).toHaveLength(3);
    expect(screen.getByText("Not sampled")).toBeVisible();
    // A healthy zero still reads as the number it is: the listener probe is
    // absent from the list, so its reading is confirmed.
    expect(screen.getByText("Not ready")).toBeVisible();
  });

  it("shows a healthy zero as a confirmed reading", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({ value: scriptedStatus({ catalogVersion: 0, migrationCurrent: 0, migrationLatest: 0 }) });
    harness.script.health.push({ value: scriptedHealth() });
    renderAdmin(harness);

    expect(await screen.findByText("0 / 0")).toBeVisible();
    expect(screen.queryByText("Unavailable")).not.toBeInTheDocument();
    expect(screen.queryByText("Not sampled")).not.toBeInTheDocument();
  });

  it("distinguishes a healthy diagnosis from one that never completed", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({ value: scriptedStatus() });
    harness.script.health.push({ value: scriptedHealth({ lastSuccessAt: "2026-09-01T00:04:00Z" }) });
    renderAdmin(harness);

    expect(await screen.findByText("No drift detected")).toBeVisible();
    expect(screen.getByText("No findings reported.")).toBeVisible();
    expect(screen.getByText("2026-09-01T00:04:00Z")).toBeVisible();
  });

  it("reports an undiagnosed schema as not diagnosed rather than as healthy", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({ value: scriptedStatus() });
    harness.script.health.push({
      value: scriptedHealth({
        state: "unavailable", lastSuccessAt: "", retries: 4,
        unavailable: [{ component: "doctor", code: "timeout" }],
      }),
    });
    renderAdmin(harness);

    expect(await screen.findByText("Not diagnosed")).toBeVisible();
    expect(screen.queryByText("No drift detected")).not.toBeInTheDocument();
    expect(screen.getByText("Never")).toBeVisible();
    expect(screen.getByText("4")).toBeVisible();
  });

  it("shows the drift findings and says when the list is only part of the report", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({ value: scriptedStatus() });
    harness.script.health.push({
      value: scriptedHealth({
        state: "drift", findingCount: 40, retries: 0,
        findings: [
          { severity: "error", code: "collection_table_missing", message: "A catalog collection table is missing" },
          { severity: "error", code: "collection_index_missing", message: "A catalog field index is missing" },
        ],
      }),
    });
    renderAdmin(harness);

    expect(await screen.findByText("Schema drift detected")).toBeVisible();
    expect(screen.getByText("collection_table_missing")).toBeVisible();
    expect(screen.getByText("A catalog field index is missing")).toBeVisible();
    expect(screen.getByText(/Showing 2 of 40 findings/)).toBeVisible();
  });

  it("does not claim a truncated report when every finding is shown", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({ value: scriptedStatus() });
    harness.script.health.push({
      value: scriptedHealth({
        state: "drift", findingCount: 1,
        findings: [{ severity: "error", code: "schema_state_stale", message: "Schema state does not match schema migration history" }],
      }),
    });
    renderAdmin(harness);

    expect(await screen.findByText("Schema drift detected")).toBeVisible();
    expect(screen.queryByText(/Showing 1 of/)).not.toBeInTheDocument();
  });
});
