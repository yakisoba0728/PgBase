#!/usr/bin/env python3
"""Analyze complete ABBA load blocks without pooling correlated requests.

Usage: python3 scripts/analyze_repeated_load.py RAW_DIRECTORY OUTPUT_DIRECTORY
Inputs are the unchanged query/mixed logs and ledger.json from the registered
experiment. Every expected workload must exist in all twelve successful runs.
"""

import argparse
import csv
import json
import math
from pathlib import Path
import random
import re
import statistics
import sys


def duration_ms(value):
    units = {"h": 3600000, "m": 60000, "s": 1000, "ms": 1,
             "us": 0.001, "µs": 0.001, "ns": 0.000001}
    matches = re.findall(r"([0-9.]+)(ns|us|µs|ms|s|m|h)", value)
    if not matches or "".join(a + b for a, b in matches) != value:
        raise ValueError("invalid Go duration: " + value)
    return sum(float(number) * units[unit] for number, unit in matches)


def read_query(path):
    result = {}
    for line in path.read_text().splitlines():
        match = re.match(r"BenchmarkConcurrentRecordReads/rows=(\d+)/([^/]+)/concurrency=(\d+)-\d+\s+(\d+)\s+(.+)", line)
        if not match:
            continue
        rows, name, concurrency, samples, rest = match.groups()
        words = rest.split()
        if len(words) % 2:
            raise ValueError("unpaired benchmark metrics: " + line)
        metrics = {words[i+1]: float(words[i]) for i in range(0, len(words), 2)}
        metrics.update(rows=int(rows), samples=int(samples))
        key = f"native/{name}/c{concurrency}"
        if key in result:
            raise ValueError("duplicate workload: " + key)
        if int(samples) != 1000 or metrics["errors"] != 0:
            raise ValueError("incomplete or failed native measurement")
        result[key] = metrics
    if len(result) != 12:
        raise ValueError(f"{path}: expected 12 native cases, found {len(result)}")
    return result


def read_http(path, expected_samples=1000):
    result, quantiles = {}, {}
    for line in path.read_text().splitlines():
        if "kind=all " in line:
            values = dict(re.findall(r"(\w+)=([^\s]+)", line))
            quantiles = {key + "-ms": duration_ms(values[key]) for key in ("p50", "p95", "p99")}
        if "workers=" not in line or "unexpected_errors=" not in line:
            continue
        values = dict(re.findall(r"(\w+)=([^\s]+)", line))
        if int(values["requests"]) != expected_samples or int(values["unexpected_errors"]) != 0:
            raise ValueError("incomplete or failed HTTP measurement")
        key = "http/c" + values["workers"]
        if key in result or not quantiles:
            raise ValueError("duplicate/missing HTTP stage")
        metrics = dict(quantiles)
        metrics.update(samples=int(values["requests"]), **{
            "queries/s": float(values["rps"]),
            "B/op": float(values["allocated_bytes_per_request"]),
            "allocs/op": float(values["allocations_per_request"]),
            "gc-cycles": float(values["gc_cycles"]),
            "gc-pause-ms": duration_ms(values["gc_pause"]),
            "pool-waits/op": float(values["pool_wait_count"])/expected_samples,
            "pool-wait-ns/op": duration_ms(values["pool_wait"])*1000000/expected_samples,
            "pool-canceled": float(values["pool_canceled"]), "errors": 0,
        })
        result[key], quantiles = metrics, {}
    if len(result) != 4:
        raise ValueError(f"{path}: expected 4 HTTP stages, found {len(result)}")
    return result


def percentile(values, fraction):
    position = (len(values)-1)*fraction
    lower = math.floor(position)
    upper = math.ceil(position)
    return values[lower] + (values[upper]-values[lower])*(position-lower)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("output", type=Path)
    kinds = parser.add_mutually_exclusive_group()
    kinds.add_argument("--http-only", action="store_true")
    kinds.add_argument("--query-only", action="store_true")
    parser.add_argument("--http-cycles", type=int, default=100)
    options = parser.parse_args()
    source, output = options.source, options.output
    ledger = json.loads((source / "ledger.json").read_text())
    if len(ledger) != (12 if options.http_only or options.query_only else 24) or any(row["exit_code"] != 0 for row in ledger):
        raise ValueError("require all registered invocations to succeed; failures must be investigated, not discarded")
    raw = {}
    expected = ["before", "after", "after", "before"] * 3
    for row in ledger:
        if row["side"] != expected[row["index"]-1]:
            raise ValueError("execution order differs from registered ABBA protocol")
        path = source / (row["name"] + ".log")
        values = read_query(path) if row["kind"] == "query" else read_http(path, options.http_cycles*10)
        for key, metrics in values.items():
            positions = raw.setdefault(key, {})
            if row["index"] in positions:
                raise ValueError("duplicate invocation")
            positions[row["index"]] = {"side": row["side"], "metrics": metrics}
    if len(raw) != (4 if options.http_only else 12 if options.query_only else 16) or any(len(values) != 12 for values in raw.values()):
        raise ValueError("missing workload observations")
    rng = random.Random(20261116)
    resamples = [[rng.randrange(6) for _ in range(6)] for _ in range(20000)]
    pairs = [(1, 2), (4, 3), (5, 6), (8, 7), (9, 10), (12, 11)]
    comparisons = []
    for workload, positions in sorted(raw.items()):
        for metric in ("p50-ms", "p95-ms", "p99-ms", "queries/s", "B/op", "allocs/op", "gc-cycles", "gc-pause-ms", "pool-waits/op", "pool-wait-ns/op"):
            before = [positions[a]["metrics"][metric] for a, _ in pairs]
            after = [positions[b]["metrics"][metric] for _, b in pairs]
            row = {"workload": workload, "metric": metric, "before": before, "after": after,
                   "before_median": statistics.median(before), "after_median": statistics.median(after),
                   "before_range": [min(before),max(before)], "after_range": [min(after),max(after)]}
            if all(value > 0 for value in before + after):
                logs = [math.log(b/a) for a, b in zip(before, after)]
                samples = sorted(math.exp(sum(logs[i] for i in sample)/6) for sample in resamples)
                ratio = math.exp(statistics.mean(logs))
                low, high = percentile(samples, .025), percentile(samples, .975)
                throughput = metric == "queries/s"
                classification = "inconclusive"
                if .9 < low and high < 1.1:
                    classification = "bounded_10pct_equivalence"
                elif (high < 1 and not throughput) or (low > 1 and throughput):
                    classification = "improvement"
                elif (low > 1 and not throughput) or (high < 1 and throughput):
                    classification = "regression"
                if metric == "p99-ms" and ratio >= 1.25 and low > 1:
                    classification = "severe_regression"
                if throughput and ratio <= .85 and high < 1:
                    classification = "severe_regression"
                row.update(paired_ratio=ratio, paired_ci95=[low,high],classification=classification)
            else:
                row.update(paired_ratio=None, paired_ci95=None, classification="zero_baseline_or_candidate")
            comparisons.append(row)
    output.mkdir(parents=True, exist_ok=True)
    report = {"method":"paired geometric ratios; run-level percentile bootstrap; six AB/BA pairs;20,000 resamples;seed20261116",
              "limitations":"Shared host, six pairs, multiple secondary endpoints; no absence-of-regression inference from wide intervals.",
              "primary_workloads":["http/c32"] if options.http_only else ["native/list_50/c1"] if options.query_only else ["native/list_50/c1","http/c32"],"pairs":pairs,"raw":raw,"comparisons":comparisons}
    (output / "comparison.json").write_text(json.dumps(report, indent=2)+"\n")
    with (output / "comparison.csv").open("w",newline="") as stream:
        writer=csv.writer(stream)
        writer.writerow(["workload","metric","before_median","after_median","ratio","ci95_low","ci95_high","classification"])
        for row in comparisons:
            writer.writerow([row["workload"],row["metric"],row["before_median"],row["after_median"],row["paired_ratio"],*(row["paired_ci95"] or [None,None]),row["classification"]])
    for row in comparisons:
        if row["workload"] in report["primary_workloads"] and row["metric"] in ("p99-ms","queries/s","B/op"):
            print(json.dumps(row))


if __name__ == "__main__":
    main()
