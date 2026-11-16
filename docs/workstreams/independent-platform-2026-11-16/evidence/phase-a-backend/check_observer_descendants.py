#!/usr/bin/env python3
"""Independent real-process check of the two leader/descendant cleanup cases."""

import ctypes
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time


spec = importlib.util.spec_from_file_location("observer", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
# Adopt our fixture's orphans so evidence collection leaves no zombie fixtures.
if ctypes.CDLL(None).prctl(36, 1, 0, 0, 0) != 0:
    raise RuntimeError("cannot enable child subreaping for isolated fixture")

results = []
for early in [True, False]:
    with tempfile.TemporaryDirectory() as directory:
        pidpath, ready = Path(directory) / "child.pid", Path(directory) / "ready"
        child = (
            "import os, signal, time, pathlib, sys; "
            "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
            "pathlib.Path(sys.argv[1]).write_text(str(os.getpid())); time.sleep(60)"
        )
        parent = (
            "import subprocess,sys,pathlib,time; "
            "subprocess.Popen([sys.executable,'-c',sys.argv[1],sys.argv[2]]); "
            "p=pathlib.Path(sys.argv[2])\n"
            "while not p.exists(): time.sleep(.01)\n"
            "pathlib.Path(sys.argv[3]).write_text('ready')\n"
            + ("raise SystemExit(0)" if early else "time.sleep(60)")
        )
        process = subprocess.Popen(
            [sys.executable, "-c", parent, child, str(pidpath), str(ready)],
            start_new_session=True,
        )
        child_pid = None
        try:
            deadline = time.monotonic() + 3
            while not ready.exists():
                if time.monotonic() > deadline:
                    raise RuntimeError("fixture readiness timeout")
                time.sleep(.01)
            child_pid = int(pidpath.read_text())
            if early:
                process.wait(timeout=1)
            started = time.monotonic()
            module.stop_process(process, grace_seconds=.1)
            elapsed = time.monotonic() - started
            # A successful kill may briefly leave a zombie until our subreaper
            # wait below. Only a live descendant is a cleanup failure.
            stat = Path(f"/proc/{child_pid}/stat")
            deadline = time.monotonic() + .2
            while True:
                state = stat.read_text().rsplit(")", 1)[1].split()[0] if stat.exists() else "gone"
                if state in ("gone", "Z", "X") or time.monotonic() >= deadline:
                    break
                time.sleep(.005)
            results.append({
                "case": "leader_already_exited" if early else "leader_exits_on_term",
                "leader_returncode": process.poll(),
                "descendant_state_after_cleanup": state,
                "descendant_running": state not in ("gone", "Z", "X"),
                "cleanup_seconds": elapsed,
            })
        finally:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait(timeout=1)
            if child_pid is not None:
                try:
                    os.waitpid(child_pid, 0)
                except ChildProcessError:
                    pass

leaked = any(result["descendant_running"] for result in results)
print(json.dumps({
    "source_commit": sys.argv[2],
    "result": "fail" if leaked else "pass",
    "results": results,
    "fixture_cleanup": "Own process groups stopped and adopted children reaped",
}, indent=2))
sys.exit(int(leaked))
