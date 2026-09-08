import { fireEvent, render, screen } from "@testing-library/react";
import { RecordRulesEditor } from "./record-rules-editor";
import { backendScript, callsTo } from "../test/backend-script";
import { memoryBackend } from "../test/memory-backend";
import { memoryHistory } from "../test/memory-history";
import { sessionWrapper } from "../test/harness";

it("edits a preset and saves the typed rules revision", async () => {
  const script = backendScript();
  const owner = { list: { op: "owner" }, view: { op: "owner" }, create: { op: "owner" }, update: { op: "owner" }, delete: { op: "owner" } };
  script.getRules.push({ value: { collection: "notes", rules: owner, revision: 4 } });
  script.setRules.push({ value: { collection: "notes", rules: { ...owner, list: { op: "allow" } }, revision: 5 } });
  render(<RecordRulesEditor collection="notes" />, { wrapper: sessionWrapper(memoryBackend(script), memoryHistory()) });
  const selects = await screen.findAllByRole("combobox");
  fireEvent.change(selects[0], { target: { value: "allow" } });
  fireEvent.click(screen.getByRole("button", { name: "Save rules" }));
  expect(await screen.findByRole("button", { name: "Save rules" })).toBeEnabled();
});

it("previews unsaved rules for bounded existing records", async () => {
  const script = backendScript();
  const owner = { list: { op: "owner" }, view: { op: "owner" }, create: { op: "owner" }, update: { op: "owner" }, delete: { op: "owner" } };
  script.getRules.push({ value: { collection: "notes", rules: owner, revision: 4 } });
  script.previewRule.push({ value: { items: [{ recordId: "0199ab10-0000-7000-8000-000000000111", allowed: true }] } });
  render(<RecordRulesEditor collection="notes" />, { wrapper: sessionWrapper(memoryBackend(script), memoryHistory()) });
  await screen.findByRole("button", { name: "Preview access" });
  fireEvent.change(screen.getByLabelText("User ID (optional for anonymous)"), { target: { value: "0199ab10-0000-7000-8000-000000000001" } });
  fireEvent.change(screen.getByLabelText("Record IDs (comma or space separated)"), { target: { value: "0199ab10-0000-7000-8000-000000000111" } });
  fireEvent.click(screen.getByRole("button", { name: "Preview access" }));
  expect(await screen.findByText(/allowed/)).toBeVisible();
  expect(callsTo(script, "previewRule")[0]?.detail).toMatchObject({
    collection: "notes",
    value: { action: "view", userId: "0199ab10-0000-7000-8000-000000000001", recordIds: ["0199ab10-0000-7000-8000-000000000111"] },
  });
});
