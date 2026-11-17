#!/usr/bin/env python3
"""Bounded offline comparison of fixed-arrival traces; no causal experiment."""

import argparse
from collections import Counter
import csv
from datetime import datetime, timezone
import hashlib
import io
import json
from pathlib import Path
import re
import statistics
import zipfile


def quantiles(values):
    ordered = sorted(values)
    if not ordered:
        return None
    return {"p50_ns": ordered[(len(ordered)*50+99)//100-1],
            "p95_ns": ordered[(len(ordered)*95+99)//100-1],
            "p99_ns": ordered[(len(ordered)*99+99)//100-1], "max_ns": ordered[-1]}


def inspect(archive_path):
    buckets = [{"start_seconds": i*10, "request_count": 0, "over_250ms": 0,
                "latency": [], "service": [], "queue": [], "generator": [],
                "pool_full_samples": 0, "db_samples": 0, "app_waits": Counter(),
                "blocked_app_samples": 0, "max_app_query_age_ms": 0,
                "host_cpu_idle": [], "host_io_wait": []} for i in range(60)]
    with zipfile.ZipFile(archive_path) as archive:
        db = json.loads(archive.read("load-evidence/open-loop-database.json"))
        log = archive.read("open-loop-http.txt").decode()
        env = archive.read("mixed-environment.txt").decode()
        vmstat = archive.read("load-evidence/host-vmstat.txt").decode()
        with archive.open("load-evidence/open-loop-samples.csv") as stream:
            for row in csv.DictReader(io.TextIOWrapper(stream)):
                bucket = buckets[int(row["scheduled_offset_ns"]) // 10_000_000_000]
                bucket["request_count"] += 1
                bucket["over_250ms"] += int(row["scheduled_to_validated_ns"]) > 250_000_000
                for target, field in [("latency", "scheduled_to_validated_ns"), ("service", "http_service_ns"), ("queue", "worker_queue_ns"), ("generator", "generator_ns")]:
                    bucket[target].append(int(row[field]))
    wait_totals, io_queries = Counter(), Counter()
    for sample in db["observations"]:
        index = (sample["scheduled_unix_ns"]-db["load_start_unix_ns"]) // 10_000_000_000
        if not 0 <= index < len(buckets):
            continue
        bucket = buckets[index]
        bucket["db_samples"] += 1
        bucket["pool_full_samples"] += sample["pool"]["AcquiredConns"] == 4
        for backend in sample["backends"]:
            if backend["application"] != "pgbase" or backend["state"] != "active":
                continue
            wait = backend["wait_type"] + "/" + backend["wait"]
            bucket["app_waits"][wait] += 1
            wait_totals[wait] += 1
            bucket["blocked_app_samples"] += bool(backend["blocking_pids"])
            bucket["max_app_query_age_ms"] = max(bucket["max_app_query_age_ms"], backend["active_ms"])
            if backend["wait_type"] in {"IO", "LWLock", "Lock"}:
                io_queries[(wait, backend["query_prefix"])] += 1
    labels = None
    swap_max = {"swpd": 0, "si": 0, "so": 0}
    for line in vmstat.splitlines():
        fields = line.split()
        if fields[:2] == ["r", "b"]:
            if fields[-1] != "UTC":
                raise ValueError("unexpected vmstat timezone")
            labels = fields[:-1]
            continue
        if labels is None or len(fields) != len(labels) + 2:
            continue
        timestamp = datetime.fromisoformat(" ".join(fields[-2:])).replace(tzinfo=timezone.utc)
        offset = int(timestamp.timestamp()*1_000_000_000)-db["load_start_unix_ns"]
        if not 0 <= offset < db["elapsed_ns"]:
            continue
        index = min(offset//10_000_000_000, 59)
        values = dict(zip(labels, map(int, fields[:-2])))
        buckets[index]["host_cpu_idle"].append(values["id"])
        buckets[index]["host_io_wait"].append(values["wa"])
        for field in swap_max:
            swap_max[field] = max(swap_max[field], values[field])
    for bucket in buckets:
        for field in ["latency", "service", "queue", "generator"]:
            bucket[field] = quantiles(bucket[field])
        bucket["host_samples"] = len(bucket["host_cpu_idle"])
        for field in ["host_cpu_idle", "host_io_wait"]:
            bucket[field+"_mean_percent"] = statistics.mean(bucket.pop(field))
        bucket["app_waits"] = dict(bucket["app_waits"])
    before, after = db["before"], db["after"]
    wal = {key: after["cluster_wal"][key]-value for key, value in before["cluster_wal"].items() if isinstance(value, (int, float))}
    wal["mean_sync_ms"] = wal["wal_sync_time"] / wal["wal_sync"] if wal["wal_sync"] else None
    io_deltas = []
    before_io = {(row["backend_type"], row["object"], row["context"]): row for row in before["cluster_io"]}
    for row in after["cluster_io"]:
        key = (row["backend_type"], row["object"], row["context"])
        previous = before_io[key]
        delta = {name: row[name]-value for name, value in previous.items() if isinstance(value, (int, float)) and isinstance(row.get(name), (int, float)) and name != "op_bytes"}
        if any(delta.values()):
            io_deltas.append({"backend_type": key[0], "object": key[1], "context": key[2], "delta": delta})
    summary_line = next(line for line in log.splitlines() if "offered_rps=" in line)
    scalars = dict(re.findall(r"(\w+)=([^\s]+)", summary_line))
    return {
        "archive_sha256": hashlib.sha256(archive_path.read_bytes()).hexdigest(),
        "environment": [line for line in env.splitlines() if re.match(r"^(go version|CPU\(s\):|Model name:|Hypervisor vendor:|Mem:|Swap:|memory=|server_version|shared_buffers|fsync|synchronous_commit)", line)],
        "load_start_utc": datetime.fromtimestamp(db["load_start_unix_ns"]/1e9,timezone.utc).isoformat(),
        "elapsed_ns": db["elapsed_ns"], "settings_before": before["settings"],
        "settings_after": after["settings"], "summary_scalars": scalars,
        "requests_over_250ms": sum(bucket["over_250ms"] for bucket in buckets),
        "pool_full_samples": sum(bucket["pool_full_samples"] for bucket in buckets),
        "db_samples": sum(bucket["db_samples"] for bucket in buckets),
        "active_application_wait_samples": dict(wait_totals),
        "top_waiting_query_samples": [{"wait": key[0], "query_prefix": key[1], "samples": value} for key,value in io_queries.most_common(12)],
        "wal_cumulative_delta": wal,
        "database_cumulative_delta": {key:after["database"][key]-value for key,value in before["database"].items() if isinstance(value,(int,float)) and key not in {"datid","numbackends"}},
        "cluster_io_cumulative_deltas": io_deltas,
        "stats_reset_unchanged": before["cluster_wal"]["stats_reset"] == after["cluster_wal"]["stats_reset"] and before["database"]["stats_reset"] == after["database"]["stats_reset"],
        "swap_max": swap_max, "buckets_10_seconds": buckets,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("historical_pass_archive", type=Path)
    parser.add_argument("final_archive", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    result = {"scope": "Two independent hosted runs; ten-second windows use intended request arrival times. DB waits are sampled events, not exact durations. Host/WAL/I/O observations support localization and hypotheses, not a causal experiment or a speed claim.",
              "historical_pass": inspect(args.historical_pass_archive), "final": inspect(args.final_archive)}
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    for name in ["historical_pass", "final"]:
        data = result[name]
        print(json.dumps({"case": name, "environment": data["environment"], "load_start_utc": data["load_start_utc"], "requests_over_250ms": data["requests_over_250ms"], "pool_full_samples": data["pool_full_samples"], "db_samples": data["db_samples"], "active_application_wait_samples": data["active_application_wait_samples"], "wal": data["wal_cumulative_delta"], "worst_windows": sorted(data["buckets_10_seconds"],key=lambda bucket:bucket["latency"]["p99_ns"],reverse=True)[:3]},indent=2))


if __name__ == "__main__":
    main()
