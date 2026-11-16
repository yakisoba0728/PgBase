#!/usr/bin/env python3
"""Independent raw-log and exact-bootstrap audit of the registered ABBA run.

Usage: python3 recompute.py RAW_DIRECTORY OUTPUT_DIRECTORY
This does not import the implementation author's analysis code. It checks its
reported observations/ratios against a second parser, enumerates all 6^6 paired
bootstrap resamples for selected endpoints, and reports the three ABBA block
geometric ratios. The latter is exploratory, not a changed success gate.
"""

import argparse
import hashlib
import itertools
import json
import math
from pathlib import Path
import re
import statistics


def milliseconds(text):
    factors = {"ns": 1e-6, "us": 1e-3, "µs": 1e-3, "ms": 1, "s": 1000, "m": 60000, "h": 3600000}
    tokens = re.findall(r"(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)", text)
    assert "".join(n + u for n, u in tokens) == text, text
    return sum(float(n) * factors[u] for n, u in tokens)


def fields(line):
    return dict(word.split("=", 1) for word in line.split() if "=" in word)


def read_run(path, kind, http_samples):
    parsed = {}
    pending = None
    content = path.read_text()
    assert "FAIL" not in content and "SKIP" not in content and "PASS" in content, path
    for line in content.splitlines():
        if kind == "query":
            parts = line.split()
            if not parts or not parts[0].startswith("BenchmarkConcurrentRecordReads/rows=") or len(parts) < 3:
                continue
            _, row_field, name, concurrency = parts[0].split("/")
            assert row_field == "rows=500000", row_field
            worker_count = concurrency.split("=")[1].split("-")[0]
            assert int(parts[1]) == 1000
            values = {parts[i + 1]: float(parts[i]) for i in range(2, len(parts), 2)}
            assert values["errors"] == 0 and values["pool-canceled"] == 0
            key = f"native/{name}/c{worker_count}"
            assert key not in parsed
            parsed[key] = values
        elif "kind=all " in line:
            pending = fields(line)
            assert int(pending["requests"]) == http_samples
        elif "workers=" in line and "unexpected_errors=" in line:
            values = fields(line)
            assert pending is not None
            assert int(values["requests"]) == http_samples
            assert int(values["unexpected_errors"]) == 0 and int(values["pool_canceled"]) == 0
            assert int(values["pool_max"]) == 4
            key = "http/c" + values["workers"]
            assert key not in parsed
            parsed[key] = {"p50-ms": milliseconds(pending["p50"]), "p95-ms": milliseconds(pending["p95"]),
                           "p99-ms": milliseconds(pending["p99"]), "queries/s": float(values["rps"]),
                           "B/op": float(values["allocated_bytes_per_request"]),
                           "allocs/op": float(values["allocations_per_request"]), "gc-cycles": float(values["gc_cycles"]),
                           "gc-pause-ms": milliseconds(values["gc_pause"]), "pool-waits/op": float(values["pool_wait_count"]) / http_samples,
                           "pool-wait-ns/op": milliseconds(values["pool_wait"]) * 1e6 / http_samples}
            pending = None
    assert len(parsed) == (12 if kind == "query" else 4), (path, len(parsed))
    return parsed


def percentile(series, probability):
    series = sorted(series)
    position = (len(series) - 1) * probability
    left = int(position)
    right = math.ceil(position)
    return series[left] + (series[right] - series[left]) * (position - left)


def exhaustive_interval(log_ratios):
    means = [math.exp(statistics.fmean(sample)) for sample in itertools.product(log_ratios, repeat=len(log_ratios))]
    return [percentile(means, .025), percentile(means, .975)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("raw", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--http-only", action="store_true")
    parser.add_argument("--http-cycles", type=int, default=100)
    options = parser.parse_args()
    raw, output = options.raw, options.output
    http_samples = options.http_cycles * 10
    output.mkdir(parents=True, exist_ok=True)
    ledger = json.loads((raw / "ledger.json").read_text())
    assert len(ledger) == (12 if options.http_only else 24) and all(entry["exit_code"] == 0 for entry in ledger)
    order = ["before", "after", "after", "before"] * 3
    observed, inputs = {}, {}
    for entry in ledger:
        index, kind, side = entry["index"], entry["kind"], entry["side"]
        assert side == order[index - 1] and kind in ("query", "mixed")
        assert entry["name"] == f"{index:02d}-{side}-{kind}"
        path = raw / (entry["name"] + ".log")
        inputs[path.name] = hashlib.sha256(path.read_bytes()).hexdigest()
        for workload, metrics in read_run(path, kind, http_samples).items():
            runs = observed.setdefault(workload, {})
            assert index not in runs
            runs[index] = metrics
    assert len(observed) == (4 if options.http_only else 16) and all(set(runs) == set(range(1, 13)) for runs in observed.values())
    pairs = [(1, 2), (4, 3), (5, 6), (8, 7), (9, 10), (12, 11)]
    prior = json.loads((raw / "analysis/comparison.json").read_text())
    selected = []
    checked = 0
    for result in prior["comparisons"]:
        workload, metric = result["workload"], result["metric"]
        before = [observed[workload][a][metric] for a, _ in pairs]
        after = [observed[workload][b][metric] for _, b in pairs]
        assert all(math.isclose(a, b, rel_tol=1e-12, abs_tol=1e-12) for a, b in zip(result["before"] + result["after"], before + after)), (workload, metric)
        checked += 1
        if not all(value > 0 for value in before + after):
            assert result["paired_ratio"] is None
            continue
        logs = [math.log(after[i] / before[i]) for i in range(6)]
        ratio = math.exp(statistics.fmean(logs))
        assert math.isclose(ratio, result["paired_ratio"], rel_tol=1e-12)
        if workload not in ("native/list_50/c1", "http/c32", "http/c8") or metric not in ("p50-ms", "p95-ms", "p99-ms", "queries/s", "B/op", "allocs/op", "pool-wait-ns/op"):
            continue
        blocks = [(logs[0] + logs[1]) / 2, (logs[2] + logs[3]) / 2, (logs[4] + logs[5]) / 2]
        selected.append({"workload": workload, "metric": metric, "before": before, "after": after,
                         "before_median": statistics.median(before), "after_median": statistics.median(after),
                         "ratio": ratio, "registered_bootstrap_ci95": result["paired_ci95"],
                         "exhaustive_paired_ci95": exhaustive_interval(logs),
                         "block_geometric_ratios": [math.exp(value) for value in blocks],
                         "author_classification": result["classification"]})
    summary = {"verified_invocations": len(ledger), "verified_native_samples": 0 if options.http_only else 144000, "verified_http_samples": 12 * 4 * http_samples,
               "errors": 0, "pool_canceled": 0, "observation_rows_crosschecked": checked,
               "pairs": pairs, "independent_method": "separate raw parser; exact enumeration of 46656 paired resamples; three block geometric ratios as sensitivity only",
               "selected_endpoints": selected, "input_sha256": inputs}
    (output / "independent-statistics.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps({key: value for key, value in summary.items() if key not in ("selected_endpoints", "input_sha256")}, indent=2))
    for row in selected:
        if row["metric"] in ("p99-ms", "queries/s", "B/op", "pool-wait-ns/op"):
            print(json.dumps(row))


if __name__ == "__main__":
    main()
