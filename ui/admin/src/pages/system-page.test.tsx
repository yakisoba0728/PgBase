import { screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { renderAdmin, scriptedAdmin } from "../test/harness";

describe("system page", () => {
  it("renders the operational status the backend reports and nothing else", async () => {
    const harness = scriptedAdmin("/admin/system");
    harness.script.status.push({
      value: {
        version: "v1.2.3",
        databaseReachable: true,
        migrationCurrent: 4,
        migrationLatest: 5,
        migrationDirty: true,
        catalogVersion: 9,
        listenerReady: false,
        // A redacted field the SystemPort type does not carry: it must not reach the page.
        internalDatabaseURL: "postgres://secret",
      } as never,
    });
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
    renderAdmin(harness);

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load system status.");
    expect(screen.queryByText("Loading system status…")).not.toBeInTheDocument();
  });
});
