#!/usr/bin/env python3
"""Offline, read-only comparison of the two retained qualification archives.

No database, HTTP request, benchmark, network request or runtime test is started.
This analysis preserves the original all-arrivals latency population and never
relabels historical or failed runs as qualification of a new source.
"""

from __future__ import annotations

import argparse
import collections
import csv
import hashlib
import io
import json
import math
from pathlib import Path
import statistics
import subprocess
import zipfile


WAL_WAITS = {"WalSync", "WALWrite", "WalWrite", "WalInitSync", "WalInitWrite"}
LATENCIES = [
    "scheduled_to_validated_ns", "http_service_ns", "generator_queue_ns",
    "generator_ns", "worker_queue_ns",
]


def quantiles(values):
    ordered = sorted(values)
    return {
        **{f"p{q}": ordered[math.ceil(len(ordered) * q / 100) - 1] for q in (50, 95, 99)},
        "max": ordered[-1], "mean": statistics.mean(ordered),
    }


def delta(before, after, keys):
    return {key: after[key] - before[key] for key in keys}


def describe(path):
    archive = path.read_bytes()
    with zipfile.ZipFile(io.BytesIO(archive)) as zf:
        assert zf.testzip() is None, f"CRC failure: {path}"
        raw_csv = zf.read("load-evidence/open-loop-samples.csv")
        raw_db = zf.read("load-evidence/open-loop-database.json")
        log = zf.read("open-loop-http.txt").decode()
        environment = zf.read("mixed-environment.txt").decode()
    rows = list(csv.DictReader(io.StringIO(raw_csv.decode())))
    db = json.loads(raw_db)
    observations = db["observations"]
    assert len(rows) == 120000
    assert [int(r["index"]) for r in rows] == list(range(120000))
    assert all(int(r["scheduled_offset_ns"]) == i * 5000000 for i, r in enumerate(rows))
    assert all(not r["error"] and int(r["status"]) > 0 for r in rows)
    expected_kind = {0: "create", 1: "update", 2: "delete", 3: "get"}
    expected_status = {"create": 201, "update": 200, "delete": 204, "get": 200, "page": 200}
    assert all(r["kind"] == expected_kind.get(i % 10, "page") and int(r["owner"]) == (i // 10) % 2 and int(r["status"]) == expected_status[r["kind"]] for i, r in enumerate(rows))
    assert all(int(r["generator_queue_ns"]) == int(r["generator_ns"]) + int(r["worker_queue_ns"]) for r in rows)
    assert all(int(r["scheduled_to_validated_ns"]) >= int(r["generator_queue_ns"]) + int(r["http_service_ns"]) for r in rows)
    assert len(observations) == 6000 and not db.get("diagnostic_error")
    assert {o["pool"]["MaxConns"] for o in observations} == {4}
    assert all(db[point]["settings"][key] == "on" for point in ("before", "after") for key in ("fsync", "synchronous_commit", "full_page_writes"))
    waits = collections.Counter()
    states = collections.Counter()
    blockers = collections.Counter()
    blocking_commit_wal_edges = 0
    active = []
    pool_full = 0
    pool_full_with_wal = 0
    pool_full_without_active = 0
    per_minute = []
    all_pool_counts = collections.Counter()
    for observation_index, observation in enumerate(observations):
        backends = observation["backends"]
        by_pid = {b["pid"]: b for b in backends}
        app = [b for b in backends if b["application"] == "pgbase"]
        app_active = [b for b in app if b["state"] == "active"]
        all_pool_counts[observation["pool"]["AcquiredConns"]] += 1
        states.update(b["state"] for b in app)
        if observation["pool"]["AcquiredConns"] == 4:
            pool_full += 1
            pool_full_with_wal += any(b["wait"] in WAL_WAITS for b in app_active)
            pool_full_without_active += not app_active
        for b in app_active:
            waits[(b["wait_type"], b["wait"], b["query_prefix"])] += 1
            active.append({**b, "observation_index": observation_index, "observed_offset_ns": observation["started_unix_ns"] - db["load_start_unix_ns"]})
        for b in app:
            for blocker_pid in b["blocking_pids"]:
                blocker = by_pid.get(blocker_pid, {})
                blockers[(b["query_prefix"], blocker.get("query_prefix", "UNKNOWN"), blocker.get("state", "UNKNOWN"), blocker.get("wait_type", ""), blocker.get("wait", ""))] += 1
                blocking_commit_wal_edges += blocker.get("query_prefix") == "commit" and blocker.get("wait") in WAL_WAITS
    for minute in range(10):
        chunk = rows[minute * 12000:(minute + 1) * 12000]
        obs = [o for o in observations if minute * 60 <= (o["started_unix_ns"] - db["load_start_unix_ns"]) / 1e9 < (minute + 1) * 60]
        per_minute.append({
            "start_seconds": minute * 60,
            "arrivals": len(chunk),
            "requests_over_250ms": sum(int(r["scheduled_to_validated_ns"]) > 250000000 for r in chunk),
            "end_to_end_ns": quantiles([int(r["scheduled_to_validated_ns"]) for r in chunk]),
            "worker_queue_ns": quantiles([int(r["worker_queue_ns"]) for r in chunk]),
            "pool_full_observations": sum(o["pool"]["AcquiredConns"] == 4 for o in obs),
        })
    wb, wa = db["before"]["cluster_wal"], db["after"]["cluster_wal"]
    wal_delta = delta(wb, wa, ["wal_bytes", "wal_records", "wal_fpi", "wal_write", "wal_sync", "wal_write_time", "wal_sync_time", "wal_buffers_full"])
    wal_delta["mean_sync_ms"] = wal_delta["wal_sync_time"] / wal_delta["wal_sync"]
    db_delta = delta(db["before"]["database"], db["after"]["database"], ["xact_commit", "xact_rollback", "tup_inserted", "tup_updated", "tup_deleted", "deadlocks", "conflicts", "sessions", "temp_files", "temp_bytes", "active_time", "idle_in_transaction_time"])
    max_active = max(active, key=lambda b: b["active_ms"])
    return {
        "archive": str(path),
        "sha256": hashlib.sha256(archive).hexdigest(),
        "member_sha256": {"load-evidence/open-loop-samples.csv": hashlib.sha256(raw_csv).hexdigest(), "load-evidence/open-loop-database.json": hashlib.sha256(raw_db).hexdigest()},
        "runtime_log_summary": [line.strip() for line in log.splitlines() if "model=open-loop" in line or "FAIL" in line or "PASS" in line],
        "hardware": [line.strip() for line in environment.splitlines() if any(token in line for token in ["Model name:", "Hypervisor vendor:", "CPU(s):", "memory=", "go version"] )],
        "latency_ns": {name: quantiles([int(r[name]) for r in rows]) for name in LATENCIES},
        "population_checks": "PASS: 120000 consecutive arrivals, 5ms spacing, all completed without error, latency decomposition valid",
        "requests_over_250ms": sum(int(r["scheduled_to_validated_ns"]) > 250000000 for r in rows),
        "workload_by_kind_owner_status": [{"kind": k[0], "owner": k[1], "status": k[2], "count": n} for k, n in sorted(collections.Counter((r["kind"], r["owner"], r["status"]) for r in rows).items())],
        "observer_samples": len(observations),
        "observer_query_ns": {**quantiles([o["query_ns"] for o in observations]), "sum": sum(o["query_ns"] for o in observations)},
        "observer_schedule_lag_ns": quantiles([o["started_unix_ns"] - o["scheduled_unix_ns"] for o in observations]),
        "pool_acquired_sample_counts": dict(sorted(all_pool_counts.items())),
        "pool_full_samples": pool_full,
        "pool_full_with_app_wal_samples": pool_full_with_wal,
        "pool_full_without_active_app_samples": pool_full_without_active,
        "app_state_sample_counts": dict(states),
        "max_sampled_active_query": max_active,
        "active_queries_over_250ms": [{"query_prefix": q, "wait_type": wt, "wait": w, "samples": n} for (q, wt, w), n in collections.Counter((b["query_prefix"], b["wait_type"], b["wait"]) for b in active if b["active_ms"] > 250).most_common()],
        "active_queries_over_250ms_locations": [b for b in active if b["active_ms"] > 250],
        "top_active_wait_samples": [{"wait_type": wt, "wait": w, "query_prefix": q, "samples": n} for (wt, w, q), n in waits.most_common(15)],
        "blocking_edges_observed": sum(blockers.values()),
        "blocking_commit_wal_edges_observed": blocking_commit_wal_edges,
        "top_blocking_edges": [{"waiter_query": k[0], "blocker_query": k[1], "blocker_state": k[2], "blocker_wait_type": k[3], "blocker_wait": k[4], "samples": n} for k, n in blockers.most_common(12)],
        "wal_cumulative_delta": wal_delta,
        "database_cumulative_delta": db_delta,
        "durability": {point: {key: db[point]["settings"][key] for key in ("fsync", "synchronous_commit", "full_page_writes", "wal_sync_method")} for point in ("before", "after")},
        "per_minute": per_minute,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("repo", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    repo = args.repo.resolve()
    evidence = repo / "docs/workstreams/independent-platform-2026-11-16/evidence"
    old, candidate, head = "4091c3be20ec8db177886a2a85550924fce37ff5", "89e883fc700ef0250024427a1f09bb88affe0383", "77bc070047c5a19d68439b75cb7b65bcde5d1c9a"
    def git(*command):
        return subprocess.check_output(["git", "-C", str(repo), *command], text=True).strip()
    checked_paths = ["pgbase/open_loop_load_integration_test.go", "pgbase/open_loop_diagnostics_integration_test.go", "pgbase/mixed_load_integration_test.go", ".github/workflows/full-qualification.yml", "go.mod", "go.sum", "internal/database", "internal/query", "internal/recordops", "internal/membership", "internal/schema", "internal/publicauth", "internal/observability"]
    source_comparison = {path: git("diff", "--name-only", old, candidate, "--", path).splitlines() for path in checked_paths}
    changed_go = git("diff", "--name-only", old, candidate, "--", "*.go", ":(exclude)*_test.go").splitlines()
    result = {
        "scope": "Bounded offline analysis of existing bytes; no runtime change or test rerun. Query ages/waits are sampled observations, not exact durations or a unique causal explanation.",
        "historical_source": old,
        "failed_candidate_source": candidate,
        "failed_candidate_tree": git("rev-parse", candidate + "^{tree}"),
        "recovery_start_head": head,
        "recovery_start_tree": git("rev-parse", head + "^{tree}"),
        "source_path_differences_historical_to_candidate": source_comparison,
        "changed_go_production_paths_historical_to_candidate": changed_go,
        "non_document_differences_candidate_to_recovery_start": git("diff", "--name-only", candidate, head, "--", ".", ":(exclude)docs").splitlines(),
        "historical_pass": describe(evidence / "final-integrated/performance/final-gate/pgbase-final-mixed-http.zip"),
        "failed_final": describe(evidence / "final-master/final-ci/artifacts/final-mixed-http.zip"),
        "limitations": [
            "Different hosted CPUs/hosts; no controlled same-host baseline/final causal intervention.",
            "Pool statistics are captured before the activity query; fields are close observations, not an atomic global snapshot.",
            "No query_start, xact_start, per-request acquisition trace, exact COMMIT durations or process IO latency distribution was retained.",
            "Equal query hashes across consecutive samples can be different transactions; no held-connection duration is inferred from them.",
            "Observer query elapsed time includes scheduling/network/DB work; it is not CPU or isolated observer overhead, and no observer-on/off pair exists.",
            "WAL counters are cluster-wide cumulative values; database counters have PostgreSQL reporting lag and cannot be assigned exactly per HTTP request.",
            "Embedded UI assets differ, so unchanged measured Go paths do not mean complete binary identity.",
        ],
        "decision": "BLOCKED / NOT_READY: durable COMMIT waits and downstream queues are localized; a unique root cause or safely correctable software defect is not established.",
        "new_runtime_tests": 0,
        "new_final_gates": 0,
    }
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"output": str(args.output), "decision": result["decision"], "historical_p99_ns": result["historical_pass"]["latency_ns"]["scheduled_to_validated_ns"]["p99"], "failed_p99_ns": result["failed_final"]["latency_ns"]["scheduled_to_validated_ns"]["p99"], "failed_over_250ms": result["failed_final"]["requests_over_250ms"]}, indent=2))


if __name__ == "__main__":
    main()
