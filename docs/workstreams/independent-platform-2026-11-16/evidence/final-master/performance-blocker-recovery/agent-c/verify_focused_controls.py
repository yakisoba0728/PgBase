#!/usr/bin/env python3
"""Read-only independent reconciliation of Agent B's retained focused controls."""
import hashlib
import json
import subprocess
import sys
from pathlib import Path

root = Path(sys.argv[1]).resolve()
base = root / "docs/workstreams/independent-platform-2026-11-16/evidence/final-master/performance-blocker-recovery/agent-b"
source = "77bc070047c5a19d68439b75cb7b65bcde5d1c9a"
tree = "07fc4145c03d636c1416e2e838ad701cebc3b03b"
expected = {
    "TestInTxRollsBackOnError", "TestInTxRollsBackOnPanic",
    "TestInTxExpiredDeadlineRollsBackAndReleasesConnection",
    "TestListenerPreservesRequestPoolCapacityAcrossReconnectAndShutdown",
    "TestPinnedRecordCatalogBlocksSchemaDDLUntilTheRecordTransactionEnds",
    "TestReadKernelQueryCountIsBoundedByPlanShapeInsteadOfRootCount",
}


def events(name):
    return [json.loads(line) for line in (base / name).read_text().splitlines() if line.strip()]


rows = events("focused-controls-race.jsonl")
parents = [r for r in rows if r["Action"] == "pass" and r.get("Test") and "/" not in r["Test"]]
subtests = [r for r in rows if r["Action"] == "pass" and "/" in r.get("Test", "")]
packages = [r for r in rows if r["Action"] == "pass" and not r.get("Test")]
settings = {}
for phase in ["before", "after"]:
    settings[phase] = dict(line.split("|", 1) for line in (base / ("focused-postgresql-settings-"+phase+".txt")).read_text().splitlines())
listener = [r["Output"].strip() for r in rows if "request_pool_max=" in r.get("Output", "")]
runner = (base / "run-focused-controls.sh").read_text()
initial = events("focused-race.jsonl")
initial_failed = [r["Test"] for r in initial if r["Action"] == "fail" and r.get("Test")]
refused = {r["Test"] for r in initial if r.get("Test") and "connect: connection refused" in r.get("Output", "")}
source_files = [
    "internal/database/tx_integration_test.go",
    "internal/schema/listener_capacity_integration_test.go",
    "internal/schema/service_integration_test.go",
    "internal/query/read_kernel_query_count_integration_test.go",
]
blobs = {path: subprocess.check_output(["git", "-C", str(root), "rev-parse", source+":"+path], text=True).strip() for path in source_files}
checks = {
    "exact_six_expected_parents": len(parents) == 6 and {r["Test"] for r in parents} == expected,
    "three_query_subtests": len(subtests) == 3 and all(r["Test"].startswith("TestReadKernelQueryCountIsBoundedByPlanShapeInsteadOfRootCount/") for r in subtests),
    "three_expected_packages": len(packages) == 3 and {r["Package"].rsplit("/",1)[-1] for r in packages} == {"database", "schema", "query"},
    "no_fail_or_skip_events": not any(r["Action"] in {"fail", "skip"} for r in rows),
    "nine_run_events": sum(r["Action"] == "run" for r in rows) == 9,
    "go_exit_zero": (base / "focused-controls-exit-code.txt").read_text().strip() == "0",
    "stderr_empty": not (base / "focused-controls-race.stderr.txt").read_text(),
    "recorded_head_tree": (base / "focused-source.txt").read_text().splitlines() == [source, tree],
    "go_version": (base / "focused-go-version.txt").read_text().strip() == "go version go1.26.11 linux/amd64",
    "race_integration_once_command": 'test -json -race -tags=integration -p 1 -count=1 -timeout=3m' in runner,
    "gomaxprocs_four": "GOMAXPROCS=4" in runner,
    "no_load_test_selected": all(name in runner for name in expected) and "-tags=integration,load" not in runner and "TestOpenLoopHTTPQualification" not in runner,
    "durability_on_and_pg17_9": all(settings[p][n] == "on" for p in settings for n in ["fsync", "synchronous_commit", "full_page_writes"]) and all(settings[p]["server_version"] == "17.9" for p in settings),
    "listener_pool_and_held_four_twice": len(listener) == 2 and all("request_pool_max=4 held_requests=4 total_fixture_backends=5" in text and "fsync=on synchronous_commit=on full_page_writes=on" in text for text in listener),
    "no_remaining_test_database_rows": not (base / "focused-database-cleanup.txt").read_text().strip(),
    "postgres_stopped": "server stopped" in (base / "focused-fixture-teardown.txt").read_text() and "database system is shut down" in (base / "focused-fixture-postgres.log").read_text(),
    "initial_six_failures_are_connection_refused": len(initial_failed) == 6 and set(initial_failed) == refused == expected,
}
artifact_names = ["focused-controls-race.jsonl", "focused-race.jsonl", "run-focused-controls.sh", "focused-source.txt", "focused-postgresql-settings-before.txt", "focused-postgresql-settings-after.txt", "focused-database-cleanup.txt", "focused-fixture-teardown.txt", "focused-fixture-postgres.log"]
report = {
    "audit": "PASS" if all(checks.values()) else "FAIL", "checks": checks,
    "parent_tests": [r["Test"] for r in parents], "subtests": [r["Test"] for r in subtests],
    "packages": [r["Package"] for r in packages], "settings": settings,
    "listener_output": listener, "reviewed_source_blobs": blobs,
    "sha256": {name: hashlib.sha256((base/name).read_bytes()).hexdigest() for name in artifact_names},
    "scope": "Independent raw-log, runner, settings, cleanup and exact-source test review only; no new test execution or patch approval.",
    "qualification": "BLOCKED / NOT_READY",
    "limits": [
        "Recorded HEAD/tree plus current immutable source inspection are source provenance; focused-source.txt alone is not a full worktree-cleanliness attestation.",
        "Only listener capacity explicitly forces pool four; other controls retain their existing fixture configuration.",
        "These successful unchanged-source controls are not a RED/GREEN reproduction of the588ms latency failure.",
        "PostgreSQL shutdown evidence confirms the fixture stopped; temporary data-directory deletion is not asserted.",
    ],
}
Path(sys.argv[2]).write_text(json.dumps(report, indent=2)+"\n")
print(json.dumps({"audit": report["audit"], "parent_tests": len(parents), "subtests": len(subtests), "packages": len(packages), "qualification": report["qualification"]}))
sys.exit(0 if all(checks.values()) else 1)
