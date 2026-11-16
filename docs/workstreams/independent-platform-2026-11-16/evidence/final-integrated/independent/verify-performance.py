#!/usr/bin/env python3
"""Independently reconcile the unchanged final gate's raw artifacts.

Written before the final run. This postprocessor does not generate load,
change PostgreSQL settings, or modify the production qualification test.
"""

import argparse
from collections import Counter
import csv
import hashlib
import json
from pathlib import Path
import re


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def quantiles_ns(values):
    ordered = sorted(values)
    if not ordered:
        return {"samples": 0, "p50": None, "p95": None, "p99": None}
    return {
        "samples": len(ordered),
        **{f"p{p}": ordered[(len(ordered) * p + 99) // 100 - 1] for p in (50, 95, 99)},
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--csv", type=Path, required=True)
    parser.add_argument("--database", type=Path, required=True)
    parser.add_argument("--test-log", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()

    with options.csv.open(newline="") as stream:
        rows = list(csv.DictReader(stream))
    expected_kind = ["create", "update", "delete", "get"] + ["page"] * 6
    numbers = [
        "index", "owner", "scheduled_offset_ns", "generator_queue_ns",
        "http_service_ns", "scheduled_to_validated_ns", "status",
        "generator_ns", "worker_queue_ns",
    ]
    for row in rows:
        for field in numbers:
            row[field] = int(row[field])
    dropped = [r for r in rows if r["error"] == "arrival queue full"]
    unscheduled = [r for r in rows if r["error"].startswith("arrival not scheduled:")]
    unexpected = [r for r in rows if r["error"] and r["error"] != "arrival queue full" and not r["error"].startswith("arrival not scheduled:")]
    attempted = [r for r in rows if r["http_service_ns"] > 0]
    completed = [r for r in rows if r["status"] != 0]
    errors = [r for r in rows if r["error"]]
    checks = {
        "120000_raw_arrivals": len(rows) == 120000,
        "unique_sequential_indices": [r["index"] for r in rows] == list(range(120000)),
        "200_rps_scheduled_offsets": all(r["scheduled_offset_ns"] == r["index"] * 5000000 for r in rows),
        "unchanged_workload_and_owner_mix": all(
            r["kind"] == expected_kind[r["index"] % 10]
            and r["owner"] == (r["index"] // 10) % 2 for r in rows
        ),
        "all_completed": len(completed) == 120000,
        "all_attempted": len(attempted) == 120000,
        "zero_dropped": not dropped,
        "zero_unscheduled": not unscheduled,
        "zero_errors": not errors,
        "expected_response_statuses": all(
            r["status"] == {"create": 201, "delete": 204}.get(r["kind"], 200)
            for r in completed
        ),
        "no_invented_drop_or_unscheduled_latency": all(
            r["http_service_ns"] == r["scheduled_to_validated_ns"] == r["status"] == 0
            for r in dropped + unscheduled
        ),
        "latency_accounting_consistent": all(
            r["scheduled_to_validated_ns"] >= r["generator_queue_ns"] + r["http_service_ns"]
            and r["generator_queue_ns"] == r["generator_ns"] + r["worker_queue_ns"]
            and min(r["generator_ns"], r["worker_queue_ns"]) >= 0
            for r in attempted
        ),
    }
    quantiles = {
        field: quantiles_ns([r[field] for r in attempted])
        for field in ["scheduled_to_validated_ns", "http_service_ns", "generator_queue_ns", "generator_ns", "worker_queue_ns"]
    }
    p99 = quantiles["scheduled_to_validated_ns"]["p99"]
    checks["end_to_end_p99_at_most_250ms"] = p99 is not None and p99 <= 250000000

    db = json.loads(options.database.read_text())
    before, after = db["before"]["settings"], db["after"]["settings"]
    durability = {k: {"before": before.get(k), "after": after.get(k)} for k in ["fsync", "synchronous_commit", "full_page_writes", "wal_sync_method"]}
    checks["durability_enabled_before_and_after"] = all(
        before.get(k) == after.get(k) == "on"
        for k in ["fsync", "synchronous_commit", "full_page_writes"]
    )
    checks["wal_sync_method_unchanged"] = bool(before.get("wal_sync_method")) and before["wal_sync_method"] == after.get("wal_sync_method")
    observations = db["observations"]
    checks["observer_succeeded"] = not db.get("diagnostic_error") and bool(observations)
    checks["request_pool_maximum_four"] = bool(observations) and all(
        s["pool"]["MaxConns"] == 4 and s["pool"]["TotalConns"] <= 4 and s["pool"]["AcquiredConns"] <= 4
        for s in observations
    )
    backend_counts = Counter(len(s.get("backends") or []) for s in observations)
    log = options.test_log.read_text()
    checks["test_pass_including_postload_durable_assertions"] = bool(
        re.search(r"--- PASS: TestOpenLoopHTTPQualification\s*\(", log)
    ) and not re.search(r"--- FAIL: TestOpenLoopHTTPQualification\s*\(", log)
    summaries = re.findall(r"offered_rps=(\d+) seconds=(\d+) intended=(\d+) enqueued=(\d+) attempted=(\d+) completed=(\d+) dropped=(\d+) unscheduled=(\d+) errors=(\d+)", log)
    expected_summary = [200, 600, 120000, len(rows) - len(dropped) - len(unscheduled), len(attempted), len(completed), len(dropped), len(unscheduled), len(errors)]
    checks["raw_counts_equal_test_summary"] = len(summaries) == 1 and list(map(int, summaries[0])) == expected_summary
    result = {
        "method": "Independent CSV nearest-rank percentiles over all attempted arrivals, including errored attempts; drops/unscheduled counted separately without fabricated latency.",
        "verdict": "Pass" if all(checks.values()) else "Fail",
        "checks": checks,
        "counts": {"scheduled_rows": len(rows), "attempted": len(attempted), "completed": len(completed), "dropped": len(dropped), "unscheduled": len(unscheduled), "errors_including_drops": len(errors), "unexpected_request_errors": len(unexpected), "incomplete": 120000 - len(completed)},
        "kind_counts": dict(Counter(r["kind"] for r in rows)),
        "owner_counts": dict(Counter(r["owner"] for r in rows)),
        "latency_ns": quantiles,
        "durability": durability,
        "settings_before": before,
        "settings_after": after,
        "db_observer": {"sample_count": len(observations), "backend_count_distribution_excluding_observer": dict(backend_counts), "observer_additional_connections": 1, "scope": db.get("scope"), "diagnostic_error": db.get("diagnostic_error")},
        "durable_oracle": "Successful unchanged test asserts total rows24256/create12000/update12000 and per-owner rows12128/create6000/update6000, full cursor traversal and cross-owner404. This parser does not independently query the now-disposed DB.",
        "files": {str(p): {"sha256": sha256(p), "bytes": p.stat().st_size} for p in [options.csv, options.database, options.test_log]},
    }
    options.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"verdict": result["verdict"], "counts": result["counts"], "end_to_end_ns": quantiles["scheduled_to_validated_ns"], "failed_checks": [k for k, ok in checks.items() if not ok]}))
    return 0 if all(checks.values()) else 1


if __name__ == "__main__":
    raise SystemExit(main())
