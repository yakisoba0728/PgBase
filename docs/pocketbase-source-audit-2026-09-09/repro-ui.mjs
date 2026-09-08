// Narrow function-level reproductions from the pinned upstream checkout.
// No DOM, network, dependencies, source edits, or production data are used.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const upstream = resolve(process.argv[2] || ".local/upstream/pocketbase");
const read = (path) => readFileSync(resolve(upstream, path), "utf8");
function between(source, start, end) {
  const from = source.indexOf(start);
  const to = source.indexOf(end, from + start.length);
  assert.ok(from >= 0 && to > from, "Expected pinned source boundaries");
  return source.slice(from, to);
}

const cronSource = between(
  read("ui/src/settings/crons/cronsList.js"),
  "    async function runCron(jobId)",
  "    return t.div(",
);
const cronData = { isRunning: {} };
const cronApp = {
  pb: { crons: { run: async () => { throw new Error("simulated API failure"); } } },
  toasts: { success: () => {} },
  checkApiError: () => {},
};
const runCron = new Function("data", "app", `${cronSource}; return runCron;`)(cronData, cronApp);
await assert.rejects(runCron("job"), /ApiClient is not defined/);
assert.equal(cronData.isRunning.job, true);
console.log("confirmed: cron failure throws ReferenceError and leaves the running flag set");

const pickerSource = read("ui/src/records/recordsPickerModal.js");
const stateSource = between(pickerSource, "    const data = store({", "    const watchers = [");
const selectedSource = between(pickerSource, "    async function loadSelected()", "    async function loadRecords(");
let finishRequest;
const response = new Promise((resolve) => { finishRequest = resolve; });
const settings = { collection: "items", selectedIds: ["selected"] };
const pickerApp = {
  utils: { toArray: (value) => value, filterDuplicatesByKey: (value) => value },
  store: { collections: [{ id: "items" }] },
  pb: { collection: () => ({ getFullList: () => response }) },
  checkApiError: () => {},
};
const picker = new Function("store", "settings", "app", "selectedBatchSize", "recordsPerPage",
  `${stateSource}\n${selectedSource}\nreturn {data, loadSelected};`)(
    (value) => value, settings, pickerApp, 100, 50,
  );
const pending = picker.loadSelected();
assert.equal(picker.data.isLoadingSelected, true);
assert.equal(picker.data.isLoadingPreselected, false);
assert.equal(picker.data.isLoading, false);
finishRequest([{ id: "selected" }]);
await pending;
assert.equal(picker.data.selected[0].id, "selected");
console.log("confirmed: pending preselected fetch does not set the combined loading getter");

const upsertHookSource = between(
  read("ui/src/records/recordUpsertModal.js"),
  "            onbeforeopen: () => {",
  "            onafteropen:",
);
const makeBeforeOpen = new Function("modalSettings", "initRecord", "rawRecord",
  `return ({${upsertHookSource}}).onbeforeopen;`);
makeBeforeOpen({}, () => {}, {})(); // Optional absent callback short-circuits.
let callbackCalled = false;
const withCallback = makeBeforeOpen({ onbeforeopen: () => { callbackCalled = true; } }, () => {}, {});
assert.throws(() => withCallback({ id: "modal" }), /el is not defined/);
assert.equal(callbackCalled, false);
console.log("confirmed: supplied record-upsert before-open callback fails on an undeclared el");
