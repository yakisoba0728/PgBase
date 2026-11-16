#!/usr/bin/env python3
"""Repeat the historical native A/B comparison with explicit fixture statistics.

Requires a disposable PGBASE_TEST_ADMIN_URL and go/psql on PATH. All invocations
use the exact current harness bytes; source patches, profiles and failed output
are retained. The original historical experiment is never rewritten.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import time

from repeat_http_regression import BASE, PATCHES


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    if not os.environ.get("PGBASE_TEST_ADMIN_URL"):
        parser.error("PGBASE_TEST_ADMIN_URL is required")
    repo = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    temporary = Path(tempfile.mkdtemp(prefix="pgbase-native-abba-"))
    harness_path = "internal/query/load_benchmark_integration_test.go"
    harness = (repo / harness_path).read_bytes()
    env = dict(os.environ, GOMAXPROCS="4", PGBASE_BENCH_ROWS="500000", PGBASE_BENCH_WARMUP="100",
               PGBASE_BENCH_EXPLAIN="1", PGBASE_BENCH_SAMPLES="1", PGBASE_BENCH_FILTER_INDEX="0", PGBASE_BENCH_INDEX_WRITE="0")

    def git(*command, cwd=repo):
        return subprocess.check_output(["git", *command], cwd=cwd)

    metadata = {"platform": platform.platform(), "cpu_count": os.cpu_count(),
                "go": subprocess.check_output(["go", "version"], text=True).strip(),
                "harness_source_head": git("rev-parse", "HEAD").decode().strip(),
                "harness_sha256": hashlib.sha256(harness).hexdigest(), "baseline": BASE, "patches": PATCHES,
                "order": ["before", "after", "after", "before"] * 3,
                "settings": {key: value for key, value in env.items() if key.startswith("PGBASE_BENCH_") or key == "GOMAXPROCS"},
                "started": time.time(), "phase": "build"}
    worktrees, ledger = [], []

    def write_metadata():
        (output / "environment.json").write_text(json.dumps(metadata, indent=2) + "\n")

    write_metadata()
    try:
        sql = "SELECT version(); SELECT name||'='||setting FROM pg_settings WHERE name IN ('shared_buffers','max_connections','work_mem','maintenance_work_mem','effective_cache_size','max_parallel_workers_per_gather','fsync','synchronous_commit','track_io_timing');"
        metadata["postgres"] = subprocess.check_output(["psql", env["PGBASE_TEST_ADMIN_URL"], "-XAtc", sql], text=True)
        for side in ("before", "after"):
            work = temporary / side
            git("worktree", "add", "--detach", str(work), BASE)
            worktrees.append(work)
            if side == "after":
                for commit in PATCHES:
                    subprocess.run(["git", "apply"], cwd=work, input=git("show", commit, "--format="), check=True)
            (work / harness_path).write_bytes(harness)
            (output / f"source-{side}.patch").write_bytes(git("diff", "HEAD", cwd=work))
            write_metadata()
            subprocess.run(["go", "test", "-c", "-tags=integration", "./internal/query", "-o", str(temporary / f"{side}.test")], cwd=work, env=env, check=True)
        metadata["phase"] = "measurement"
        write_metadata()
        for index, side in enumerate(metadata["order"], 1):
            name = f"{index:02d}-{side}-query"
            command = [str(temporary / f"{side}.test"), "-test.run=^$", "-test.bench=^BenchmarkConcurrentRecordReads$", "-test.benchtime=1000x",
                       "-test.benchmem", "-test.v", "-test.count=1", "-test.timeout=15m", f"-test.cpuprofile={output/name}.cpu", f"-test.memprofile={output/name}.mem"]
            record = {"index": index, "side": side, "kind": "query", "name": name, "command": command, "started": time.time()}
            print("START", name, flush=True)
            with (output / f"{name}.log").open("w") as log:
                process = subprocess.Popen(command, cwd=temporary/side, env=env, stdout=log, stderr=subprocess.STDOUT)
                _, status, usage = os.wait4(process.pid, 0)
                process.returncode = os.waitstatus_to_exitcode(status)
            record.update(exit_code=process.returncode, elapsed_seconds=time.time()-record["started"], user_seconds=usage.ru_utime,
                          system_seconds=usage.ru_stime, maxrss_kib=usage.ru_maxrss, voluntary_context_switches=usage.ru_nvcsw, involuntary_context_switches=usage.ru_nivcsw)
            ledger.append(record)
            (output/"ledger.json").write_text(json.dumps(ledger, indent=2)+"\n")
            print("END", name, "exit", process.returncode, "seconds", round(record["elapsed_seconds"], 3), flush=True)
            if process.returncode:
                raise RuntimeError(f"{name} failed; preserve all evidence before investigating")
        metadata["phase"] = "analysis"
        write_metadata()
        subprocess.run(["python3", str(repo/"scripts/analyze_repeated_load.py"), str(output), str(output/"analysis"), "--query-only"], cwd=repo, check=True)
        metadata["phase"] = "complete"
    except BaseException as error:
        metadata["failure"] = {"type": type(error).__name__, "message": str(error)}
        raise
    finally:
        metadata["ended"] = time.time()
        write_metadata()
        hashes = {str(path.relative_to(output)): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(output.rglob("*")) if path.is_file()}
        (output/"manifest.json").write_text(json.dumps(hashes, indent=2)+"\n")
        for work in worktrees:
            subprocess.run(["git", "worktree", "remove", "--force", str(work)], cwd=repo, check=False)


if __name__ == "__main__":
    main()
