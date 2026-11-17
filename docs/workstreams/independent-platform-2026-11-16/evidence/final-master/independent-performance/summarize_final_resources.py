#!/usr/bin/env python3
"""Summarize existing resource observations; never starts processes or load."""

import argparse
from collections import Counter
from datetime import datetime, timezone
from decimal import Decimal
import hashlib
import json
from pathlib import Path
import re
import statistics


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def summarize_vmstat(text, start_ns, elapsed_ns):
    labels = None
    observations = []
    aligned = []
    for line in text.splitlines():
        fields = line.split()
        if fields[:2] == ["r", "b"]:
            if fields[-1] != "UTC":
                raise ValueError("vmstat timestamps are not explicitly UTC")
            labels = fields[:-1]
            continue
        if labels is None or len(fields) != len(labels) + 2:
            continue
        timestamp = datetime.fromisoformat(" ".join(fields[-2:])).replace(tzinfo=timezone.utc)
        sample = dict(zip(labels, map(int, fields[:-2])))
        sample["timestamp_ns"] = int(timestamp.timestamp() * 1_000_000_000)
        observations.append(sample)
        # vmstat's initial row is a since-boot average, not a one-second sample.
        if len(observations) > 1 and start_ns <= sample["timestamp_ns"] < start_ns + elapsed_ns:
            aligned.append(sample)
    if not aligned:
        raise ValueError("no timestamp-aligned vmstat interval observations")
    return {
        "scope": "Host-wide interval samples aligned to the fixed-arrival load; first since-boot row excluded. Not application-only CPU or RSS.",
        "all_observations": len(observations),
        "aligned_observations": len(aligned),
        "first_timestamp_ns": aligned[0]["timestamp_ns"],
        "last_timestamp_ns": aligned[-1]["timestamp_ns"],
        "cpu_mean_percent": {key: statistics.mean(row[key] for row in aligned) for key in ["us", "sy", "id", "wa", "st"]},
        "swap_max": {key: max(row[key] for row in aligned) for key in ["swpd", "si", "so"]},
        "run_queue_max": max(row["r"] for row in aligned),
        "blocked_process_max": max(row["b"] for row in aligned),
    }


def display_bytes(value):
    match = re.fullmatch(r"([0-9.]+)(B|kB|MB|GB|KiB|MiB|GiB)", value.strip())
    if not match:
        raise ValueError("unsupported Docker memory quantity: " + value)
    scale = {"B": 1, "kB": 1000, "MB": 1000**2, "GB": 1000**3,
             "KiB": 1024, "MiB": 1024**2, "GiB": 1024**3}
    return int(Decimal(match[1]) * scale[match[2]])


def summarize_docker(text):
    # Preserve original bytes elsewhere. Stripping ANSI here only parses the
    # streaming display, which includes repeated redraws and no sample times.
    clean = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)
    displayed = [json.loads(line.strip()) for line in clean.splitlines() if line.strip().startswith("{")]
    frames = [frame for frame in displayed if all(frame.get(key) not in {None, "", "--"} for key in ["MemUsage", "CPUPerc", "PIDs"])]
    if not frames:
        raise ValueError("no Docker resource frames")
    memory = [display_bytes(frame["MemUsage"].split(" / ")[0]) for frame in frames]
    cpu = [float(frame["CPUPerc"].removesuffix("%")) for frame in frames]
    return {
        "scope": "PostgreSQL container display frames across the observer lifetime, including setup/drain; no timestamps permit precise load alignment. Redraw duplicates retained; maxima do not claim time averages. Docker MemUsage is not application RSS.",
        "frames_including_redraws": len(displayed),
        "frames_with_metrics": len(frames),
        "frames_with_unavailable_metrics": len(displayed) - len(frames),
        "container_ids": sorted(set(frame["ID"] for frame in frames)),
        "cpu_peak_display_percent": max(cpu),
        "memory_peak_display_bytes_approximate": max(memory),
        "memory_min_display_bytes_approximate": min(memory),
        "pids_peak": max(int(frame["PIDs"]) for frame in frames),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("raw_directory", type=Path)
    parser.add_argument("verification", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    proof = json.loads(args.verification.read_text())
    directory = args.raw_directory / "load-evidence"
    db = json.loads((directory / "open-loop-database.json").read_text())
    observations = db["observations"]
    app_pids = Counter()
    other_queries = Counter()
    for sample in observations:
        for backend in sample["backends"]:
            if backend["application"] in {"pgbase", "pgbase-backup-gate"}:
                app_pids[backend["pid"]] += 1
            else:
                other_queries[backend["query_prefix"]] += 1
    result = {
        "provenance": proof["provenance"],
        "host": summarize_vmstat((directory / "host-vmstat.txt").read_text(), db["load_start_unix_ns"], db["elapsed_ns"]),
        "database_container": summarize_docker((directory / "database-container-stats.txt").read_text()),
        "runtime": {
            "scope": "Go process includes server, HTTP client, generator and diagnostic overhead. Logged runtime CPU estimates and allocation/heap counters are not isolated server measurements.",
            "scalars": {key: proof["resource_summary_scalars"][key] for key in ["bytes_per_request", "allocs_per_request", "gc_cycles", "gc_pause", "runtime_busy_cpu_seconds", "runtime_gc_cpu_seconds", "heap_before", "heap_after", "pool_waits", "pool_wait", "pool_canceled"]},
            "application_peak_rss": None,
            "application_peak_rss_reason": "No per-process RSS series or peak RSS was recorded by this workflow.",
        },
        "connections": {
            "persistent_application_pid_sample_counts": dict(app_pids),
            "activity_backend_counts_excluding_observer": dict(Counter(len(sample["backends"]) for sample in observations)),
            "other_activity_query_counts": dict(other_queries),
            "maintenance_classification_limit": "The sampler omits backend_type. Autovacuum attribution is inferred only where its query_prefix explicitly identifies it.",
            "persistent_diagnostic_budget": "4 request pool + 1 Listener + 1 backup gate + 1 optional observer = 7",
            "additional_storage_operation_budget": "Up to 64 simultaneous dedicated coordination sessions per storage controller; this workload does not measure that worst case. Operator/migration/maintenance headroom is additional.",
        },
        "input_sha256": {str(path.relative_to(args.raw_directory)): digest(path) for path in [directory / "open-loop-database.json", directory / "host-vmstat.txt", directory / "database-container-stats.txt"]},
    }
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"aligned_host_samples": result["host"]["aligned_observations"], "host_cpu_mean_percent": result["host"]["cpu_mean_percent"], "runtime": result["runtime"]["scalars"], "database_container": result["database_container"], "application_peak_rss": None}, indent=2))


if __name__ == "__main__":
    main()
