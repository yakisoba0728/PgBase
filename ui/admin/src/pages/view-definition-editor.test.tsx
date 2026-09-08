import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { callsTo } from "../test/backend-script";
import { collection, renderAdmin, scriptedAdmin, textField } from "../test/harness";

it("previews the current view source, projection and unsaved static filter", async () => {
  const harness = scriptedAdmin("/admin/collections/new");
  harness.script.catalog.push({ value: { version: 1, collections: [collection("notes", "notes-id", [textField("title", 0)])] } });
  harness.script.plan.push({ value: { baseVersion: 1, checksum: "view-plan", sql: ["CREATE VIEW app.published AS SELECT ..."], fields: [{ name: "title", type: "text", required: false, unique: false, indexed: false }] } });
  const user = userEvent.setup();
  renderAdmin(harness);
  await screen.findByRole("heading", { name: "Create collection" });
  await user.type(screen.getByLabelText("Collection name"), "published");
  await user.selectOptions(screen.getByLabelText("Collection kind"), "view");
  await screen.findByRole("option", { name: "notes" });
  await user.selectOptions(screen.getByLabelText("Source collection"), "notes");
  await user.click(screen.getByLabelText("Project title"));
  await user.selectOptions(screen.getByLabelText("Static filter"), "compare");
  await user.selectOptions(screen.getByLabelText("Filter field"), "title");
  await user.type(screen.getByLabelText("Filter value"), "published");
  await user.click(screen.getByRole("button", { name: "Preview collection" }));
  await screen.findByText("CREATE VIEW app.published AS SELECT ...");
  expect(callsTo(harness.script, "plan")[0].detail.change).toEqual({ operation: "create", name: "published", fields: [], indexes: [], view: { source: "notes", projection: ["title"], filter: { op: "eq", field: "title", value: "published" } } });
  await user.type(screen.getByLabelText("Filter value"), " changed");
  expect(screen.queryByRole("button", { name: "Apply collection" })).not.toBeInTheDocument();
});
