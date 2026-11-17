#!/usr/bin/env python3
"""Independently reconcile sampled blockers in retained failed/passing ZIPs."""
import json
import subprocess
import sys
import zipfile
from collections import Counter
from pathlib import Path

root = Path(sys.argv[1]).resolve()
evidence = root / "docs/workstreams/independent-platform-2026-11-16/evidence"
wal_commit_waits = {"WalSync", "WALWrite", "WalWrite"}


def inspect(path):
    with zipfile.ZipFile(path) as archive:
        data = json.loads(archive.read("load-evidence/open-loop-database.json"))
    observations = data["observations"]
    full = [o for o in observations if o["pool"]["AcquiredConns"] == 4]
    edges = []
    commits = []
    for observation in observations:
        by_pid = {b["pid"]: b for b in observation["backends"]}
        for backend in observation["backends"]:
            if backend["state"] == "active" and backend["query_prefix"].strip().lower() == "commit":
                commits.append({"active_ms": backend["active_ms"], "wait": backend["wait"], "load_offset_ns": observation["started_unix_ns"] - data["load_start_unix_ns"]})
            for pid in backend["blocking_pids"]:
                edges.append((backend, by_pid.get(pid, {})))
    commit_edges = [(waiter, blocker) for waiter, blocker in edges if blocker.get("query_prefix", "").strip().lower() == "commit"]
    return {
        "archive": str(path.relative_to(root)),
        "samples": len(observations), "pool_full_samples": len(full),
        "pool_full_with_application_wal_commit_wait": sum(any(b["state"] == "active" and b["application"] == "pgbase" and b["wait"] in wal_commit_waits for b in o["backends"]) for o in full),
        "pool_full_with_wal_including_initialization": sum(any(b["application"] == "pgbase" and b["wait"] in wal_commit_waits | {"WalInitSync"} for b in o["backends"]) for o in full),
        "blocking_edges": len(edges), "edges_with_commit_blocker": len(commit_edges),
        "commit_blocker_waits": dict(Counter(blocker["wait"] for _, blocker in commit_edges)),
        "waiter_query_prefix_counts": dict(Counter(waiter["query_prefix"] for waiter, _ in edges)),
        "maximum_sampled_active_commit": max(commits, key=lambda c: c["active_ms"]),
        "observer_query_total_ns": sum(o["query_ns"] for o in observations),
        "observer_query_p99_ns": sorted(o["query_ns"] for o in observations)[(len(observations)*99+99)//100-1],
    }


current = inspect(evidence / "final-master/final-ci/artifacts/final-mixed-http.zip")
historical = inspect(evidence / "final-integrated/performance/final-gate/pgbase-final-mixed-http.zip")
reviewed = ["pgbase/open_loop_load_integration_test.go", "pgbase/open_loop_diagnostics_integration_test.go", ".github/workflows/full-qualification.yml", "internal/recordops/transaction.go", "internal/recordops/collection_mutation_lock.go"]
source = {}
for path in reviewed:
    source[path] = {name: subprocess.check_output(["git", "-C", str(root), "rev-parse", ref+":"+path], text=True).strip() for name, ref in [("historical_pass", "4091c3be20ec8db177886a2a85550924fce37ff5"), ("current_fail", "89e883fc700ef0250024427a1f09bb88affe0383")]}
checks = {
    "current_pool_full_count": current["pool_full_samples"] == 1604,
    "current_wal_commit_pool_full_count": current["pool_full_with_application_wal_commit_wait"] == 1038,
    "current_commit_blocker_edges": current["edges_with_commit_blocker"] == 117,
    "all_commit_blocker_edges_wal_related": set(current["commit_blocker_waits"]) <= wal_commit_waits,
    "current_maximum_active_commit": current["maximum_sampled_active_commit"]["active_ms"] == 284.166 and current["maximum_sampled_active_commit"]["wait"] == "WalSync",
    "reviewed_workload_transaction_sources_unchanged": all(p["historical_pass"] == p["current_fail"] for p in source.values()),
}
report = {
    "audit": "PASS" if all(checks.values()) else "FAIL", "checks": checks,
    "wal_commit_wait_set": sorted(wal_commit_waits), "wal_commit_wait_state": "active", "current": current, "historical_pass": historical,
    "reviewed_source_blobs": source,
    "conclusion": "Existing samples localize some waiting to a COMMIT holding transaction locks while waiting on WAL, followed by pool and worker queues. They do not identify why that COMMIT's WAL was slow or a removable software defect.",
    "limits": [
        "Active query age is sampled, not a full transaction trace; repeated query hashes are not request/transaction IDs.",
        "Initial cross-check counted1045 full-pool samples without an active-state condition. The final1038 count requires active: seven extra idle-in-transaction/WAL combinations are treated as sampled transitions, not new active waits.",
        "Sampled blockers can change between PostgreSQL activity fields and pg_blocking_pids reads.",
        "Observer elapsed query time is not its CPU/IO overhead, and overlap cannot establish absence of perturbation.",
        "Different hosts and absence of concurrent storage/block-device latency traces prevent sole hardware attribution.",
        "Current and historical aggregate SQL/WAL differences can include background work and differing fixture setup; no causal speedup is inferred.",
    ],
}
Path(sys.argv[2]).write_text(json.dumps(report, indent=2)+"\n")
print(json.dumps({"audit": report["audit"], "checks": checks}))
sys.exit(0 if all(checks.values()) else 1)
