import { describe, expect, it } from "vitest";
import { findOperation, operationState, trackOperation, untrackOperation } from "./operations";

describe("running operations", () => {
  it("reads progress and outcome per kind without inventing percentages", () => {
    expect(operationState("backup", { status: "running", phase: "copying_objects_3_of_12" })).toEqual({ state: "running", progress: "25%" });
    expect(operationState("backup", { status: "running", phase: "dumping_database" })).toEqual({ state: "running", progress: undefined });
    expect(operationState("restore", { status: "completed" }).state).toBe("done");
    expect(operationState("migration", { status: "copying", copiedObjects: 4, totalObjects: 10 })).toEqual({ state: "running", progress: "4/10" });
    expect(operationState("migration", { status: "canceled" }).state).toBe("failed");
    expect(operationState("migration", null).state).toBe("gone");
    expect(operationState("schema", { status: "unknown" }).state).toBe("attention");
    expect(operationState("import", { status: "committed" }).state).toBe("done");
    // Not answered yet (or a transient error): still running, never dropped.
    expect(operationState("schema", undefined).state).toBe("running");
  });

  it("persists entries and lets exactly one caller report an outcome", () => {
    trackOperation({ kind: "schema", id: "op-1", subject: "posts", checksum: "c", idempotencyKey: "k" });
    expect(findOperation("schema", "posts")?.idempotencyKey).toBe("k");
    expect(localStorage.getItem("pgbase.operations")).toContain("op-1");
    expect(untrackOperation("op-1")).toBe(true);
    expect(untrackOperation("op-1")).toBe(false);
    expect(findOperation("schema")).toBeUndefined();
  });
});
