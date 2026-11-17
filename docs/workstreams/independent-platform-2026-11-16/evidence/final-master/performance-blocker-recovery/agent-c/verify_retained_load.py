#!/usr/bin/env python3
"""Independent, read-only recovery audit; never starts a load or database.

Usage: python3 <this script> <repository root> <output.json>
The p99 failure is an expected historical outcome, not an audit failure.
"""
import csv
import hashlib
import io
import json
import math
import re
import subprocess
import sys
import zipfile
from collections import Counter
from pathlib import Path

ROOT = Path(sys.argv[1]).resolve()
OUTPUT = Path(sys.argv[2])
BASE = ROOT / "docs/workstreams/independent-platform-2026-11-16/evidence"
START = "77bc070047c5a19d68439b75cb7b65bcde5d1c9a"
CANDIDATE = "89e883fc700ef0250024427a1f09bb88affe0383"
CHECKOUT = "b5c17b6f989bb11575e83544d37cc37fc6385d19"
TREE = "c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55"


def git(*args):
    return subprocess.check_output(["git", "-C", str(ROOT), *args], text=True).strip()


def percentile(values, percent):
    values = sorted(values)
    return values[math.ceil(len(values) * percent / 100) - 1]


def audit(relative, expected_hash, expected_completed, expected_drops):
    path = BASE / relative
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    with zipfile.ZipFile(path) as archive:
        crc_ok = archive.testzip() is None
        csv_bytes = archive.read("load-evidence/open-loop-samples.csv")
        rows = list(csv.DictReader(io.StringIO(csv_bytes.decode())))
        database = json.loads(archive.read("load-evidence/open-loop-database.json"))
        summary = archive.read("open-loop-http.txt").decode()
        environment = archive.read("mixed-environment.txt").decode()
    integers = [
        "index", "owner", "scheduled_offset_ns", "generator_queue_ns",
        "http_service_ns", "scheduled_to_validated_ns", "status",
        "generator_ns", "worker_queue_ns",
    ]
    for row in rows:
        for key in integers:
            row[key] = int(row[key])
    completed = sum(row["status"] != 0 for row in rows)
    attempted = sum(row["http_service_ns"] > 0 for row in rows)
    errors = Counter(row["error"] for row in rows if row["error"])
    completed_rows = [row for row in rows if row["http_service_ns"] > 0]
    kinds = ["create", "update", "delete", "get"] + ["page"] * 6
    checks = {
        "archive_hash": digest == expected_hash,
        "archive_crc": crc_ok,
        "rows_120000": len(rows) == 120000,
        "indices_unique_complete_ordered": [r["index"] for r in rows] == list(range(120000)),
        "schedule_200rps_600s": all(r["scheduled_offset_ns"] == r["index"] * 5000000 for r in rows),
        "workload_mix_owners": all(r["kind"] == kinds[r["index"] % 10] and r["owner"] == (r["index"] // 10) % 2 for r in rows),
        "completed_expected": completed == expected_completed,
        "attempted_expected": attempted == expected_completed,
        "dropped_expected": errors["arrival queue full"] == expected_drops,
        "error_population_expected": sum(errors.values()) == expected_drops,
        "success_status_semantics": all(r["status"] == {"create": 201, "delete": 204}.get(r["kind"], 200) for r in completed_rows),
        "latency_decomposition": all(r["generator_queue_ns"] == r["generator_ns"] + r["worker_queue_ns"] and r["scheduled_to_validated_ns"] >= r["generator_queue_ns"] + r["http_service_ns"] for r in completed_rows),
        "pool4_observations": all(o["pool"]["MaxConns"] == 4 and o["pool"]["TotalConns"] <= 4 and o["pool"]["AcquiredConns"] <= 4 for o in database["observations"]),
        "retained_test_failure": "--- FAIL: TestOpenLoopHTTPQualification" in summary,
    }
    quantiles = {key: {str(p): percentile([r[key] for r in completed_rows], p) for p in (50, 95, 99)} for key in ["scheduled_to_validated_ns", "http_service_ns", "generator_queue_ns", "generator_ns", "worker_queue_ns"]}
    settings = {phase: database[phase]["settings"] for phase in ("before", "after")}
    observations = database["observations"]
    waits = Counter(b["wait"] for o in observations for b in o["backends"] if b["state"] == "active")
    return {
        "archive": str(path.relative_to(ROOT)), "sha256": digest,
        "bytes": path.stat().st_size, "csv_sha256": hashlib.sha256(csv_bytes).hexdigest(),
        "artifact_checkout": environment.splitlines()[0], "checks": checks,
        "rows": len(rows), "completed": completed, "attempted": attempted,
        "errors": dict(errors), "incomplete": len(rows)-completed,
        "quantiles_ns_attempted_population": quantiles,
        "arrivals_over_250ms": sum(r["scheduled_to_validated_ns"] > 250000000 for r in rows),
        "durability": {phase: {name: settings[phase].get(name, "NOT_RECORDED") for name in ["fsync", "synchronous_commit", "full_page_writes"]} for phase in settings},
        "observations": len(observations), "observer_interval_ms": database["interval_ms"],
        "observer_query_ns": {str(p): percentile([o["query_ns"] for o in observations], p) for p in [50,95,99]},
        "active_wait_samples": dict(waits),
        "wal_deltas": {key: database["after"]["cluster_wal"][key]-database["before"]["cluster_wal"][key] for key in ["wal_records", "wal_bytes", "wal_write", "wal_sync", "wal_write_time", "wal_sync_time"]},
        "summary": next(line.strip() for line in summary.splitlines() if "offered_rps=" in line),
    }


current = audit("final-master/final-ci/artifacts/final-mixed-http.zip", "560f35fa19fb6a18c96c7bc517759beeedb6604dc2fbdd2de044c38113e7e393", 120000, 0)
historical = audit("final-master/historical-failed-gate/original-artifact.zip", "d2411a4837ffd38a5fd3c2b67423240f07de4eed9ed0f5d4038af59ca628cf6e", 119453, 547)
current["checks"].update({
    "exact_current_checkout": current["artifact_checkout"] == CHECKOUT,
    "e2e_p99_failure_reproduced": current["quantiles_ns_attempted_population"]["scheduled_to_validated_ns"]["99"] == 588031143,
    "7726_late_arrivals": current["arrivals_over_250ms"] == 7726,
    "all_durability_on": all(value == "on" for phase in current["durability"].values() for value in phase.values()),
})
non_doc_changes = git("diff", "--name-only", CANDIDATE, START, "--", ".", ":!docs").splitlines()
source = {
    "initial_head": START, "initial_tree": git("rev-parse", START+"^{tree}"),
    "candidate": CANDIDATE, "candidate_tree": git("rev-parse", CANDIDATE+"^{tree}"),
    "checkout": CHECKOUT, "checkout_tree": git("rev-parse", CHECKOUT+"^{tree}"),
    "non_document_changes_after_candidate": non_doc_changes,
}
source["checks"] = {"equal_tested_trees": source["candidate_tree"] == source["checkout_tree"] == TREE, "source_equivalence": not non_doc_changes}
all_checks = list(source["checks"].values()) + list(current["checks"].values()) + list(historical["checks"].values())
report = {
    "audit": "PASS" if all(all_checks) else "FAIL", "qualification": "BLOCKED / NOT_READY",
    "execution_scope": "Read-only existing ZIP/CSV/source inspection; no workload, scanner or database execution",
    "source": source, "current_failed_gate": current, "historical_547_drop_failure": historical,
    "limits": [
        "Sampled wait counts and aggregate WAL timing establish association, not an exclusive causal explanation.",
        "No new runtime correction or RED/GREEN reproduction exists at this checkpoint.",
        "Historical missing full_page_writes metadata stays unknown; it is not inferred from newer source.",
        "Completed counts with zero errors do not certify data integrity alone; existing exact-source SQL/owner checks and CI logs remain required.",
    ],
}
OUTPUT.parent.mkdir(parents=True, exist_ok=True)
OUTPUT.write_text(json.dumps(report, indent=2) + "\n")
print(json.dumps({"audit": report["audit"], "qualification": report["qualification"], "current_p99_ns": current["quantiles_ns_attempted_population"]["scheduled_to_validated_ns"]["99"], "historical_drops": historical["errors"]["arrival queue full"]}))
sys.exit(0 if all(all_checks) else 1)
