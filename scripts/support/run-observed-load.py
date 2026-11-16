#!/usr/bin/env python3
"""Run a load command with bounded cleanup of its resource observers."""

import argparse
from contextlib import ExitStack
from datetime import datetime, timezone
import os
from pathlib import Path
import signal
import subprocess
import sys


def stop_process(process, grace_seconds=2):
    """Reap our child; an observer ignoring SIGTERM cannot hide the test result."""
    if process.poll() is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.wait(timeout=grace_seconds)
    except subprocess.TimeoutExpired:
        print(f"observer cleanup: force-stopping process {process.pid}", file=sys.stderr)
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=grace_seconds)


def run(command, observers, grace_seconds=2):
    """Preserve command failure; observer/cleanup failures also fail a passing run."""
    processes = []
    workload = None
    status, observer_failed, cleanup_failed = 1, False, False
    with ExitStack() as files:
        try:
            for arguments, path, minimum_lines in observers:
                output = files.enter_context(path.open("wb"))
                process = subprocess.Popen(arguments, stdout=output, start_new_session=True)
                processes.append((process, path, minimum_lines))
            workload = subprocess.Popen(command, start_new_session=True)
            status = workload.wait()
            if status < 0:
                status = 128 - status
            for process, path, minimum_lines in processes:
                if process.poll() is not None or len(path.read_bytes().splitlines()) < minimum_lines:
                    print(f"resource observer failed or produced incomplete evidence: {path}", file=sys.stderr)
                    observer_failed = True
        except OSError as error:
            print(f"load observer setup failed: {error}", file=sys.stderr)
            observer_failed = True
        finally:
            # Include the workload on cancellation; its child test binary must
            # not outlive a cancelled supervisor. Every wait is bounded.
            children = [process for process, _, _ in processes]
            if workload is not None:
                children.append(workload)
            for process in children:
                try:
                    stop_process(process, grace_seconds)
                except (OSError, subprocess.TimeoutExpired) as error:
                    print(f"resource cleanup failed: {error}", file=sys.stderr)
                    cleanup_failed = True
    return status or int(observer_failed or cleanup_failed)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--container", required=True)
    parser.add_argument("--evidence-dir", type=Path, required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    options = parser.parse_args()
    command = options.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        parser.error("a load command is required after --")
    directory = options.evidence_dir
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "host-observer-start.txt").write_text(
        datetime.now(timezone.utc).isoformat() + "\n"
    )
    # Docker's streaming output contains terminal-control bytes even when
    # redirected. Preserve it as text, not a falsely named JSONL file.
    observers = [
        (["vmstat", "-w", "-t", "1"], directory / "host-vmstat.txt", 3),
        (["docker", "stats", "--format", "{{json .}}", options.container],
         directory / "database-container-stats.txt", 1),
    ]

    def interrupted(signum, _frame):
        raise SystemExit(128 + signum)

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    return run(command, observers)


if __name__ == "__main__":
    sys.exit(main())
