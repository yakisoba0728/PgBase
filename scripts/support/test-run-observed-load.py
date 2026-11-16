#!/usr/bin/env python3
"""Real subprocess regressions for resource-observer cleanup and exit status."""

import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest

SPEC = importlib.util.spec_from_file_location(
    "load_observers", Path(__file__).with_name("run-observed-load.py")
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


class ObserverCleanupTests(unittest.TestCase):
    def run_fixture(self, exit_status, observer_exits=False):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "observer.txt"
            observer_code = (
                "import os, signal, time; "
                "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                "print(os.getpid(), flush=True); "
                + ("raise SystemExit(3)" if observer_exits else "time.sleep(60)")
            )
            # The workload waits for the observer's readiness output rather
            # than racing its SIGTERM handler. An early exit is then reaped.
            command_code = (
                "import pathlib, time, sys; "
                "p=pathlib.Path(sys.argv[1]); "
                "deadline=time.monotonic()+3\n"
                "while not p.exists() or not p.stat().st_size:\n"
                " if time.monotonic()>deadline: raise SystemExit(99)\n"
                " time.sleep(.01)\n"
                "time.sleep(.05)\n"
                f"raise SystemExit({exit_status})\n"
            )
            before = time.monotonic()
            result = MODULE.run(
                [sys.executable, "-c", command_code, str(path)],
                [([sys.executable, "-c", observer_code], path, 1)],
                grace_seconds=.1,
            )
            self.assertLess(time.monotonic() - before, 3)
            pid = int(path.read_text().strip())
            self.assertFalse(Path(f"/proc/{pid}").exists(), "observer was not reaped")
            return result

    def test_hung_observer_preserves_load_failure(self):
        self.assertEqual(self.run_fixture(17), 17)

    def test_hung_observer_does_not_turn_success_into_timeout(self):
        self.assertEqual(self.run_fixture(0), 0)

    def test_early_observer_exit_fails_successful_workload(self):
        self.assertEqual(self.run_fixture(0, observer_exits=True), 1)

    def test_workload_signal_remains_failure(self):
        self.assertEqual(MODULE.run([sys.executable, "-c", "import os, signal; os.kill(os.getpid(), signal.SIGTERM)"], []), 143)

    def check_descendant_cleanup(self, leader_exits_first):
        with tempfile.TemporaryDirectory() as directory:
            ready = Path(directory) / "child.pid"
            child_code = (
                "import os, signal, sys, time; "
                "signal.signal(signal.SIGTERM, signal.SIG_IGN); "
                "open(sys.argv[1], 'w').write(str(os.getpid())); time.sleep(60)"
            )
            leader_code = (
                "import pathlib, subprocess, sys, time; "
                "subprocess.Popen([sys.executable, '-c', sys.argv[1], sys.argv[2]])\n"
                "while not pathlib.Path(sys.argv[2]).exists(): time.sleep(.01)\n"
                + ("raise SystemExit(0)" if leader_exits_first else "time.sleep(60)")
            )
            leader = subprocess.Popen(
                [sys.executable, "-c", leader_code, child_code, str(ready)],
                start_new_session=True,
            )
            try:
                deadline = time.monotonic() + 3
                while not ready.exists() or not ready.stat().st_size:
                    self.assertLess(time.monotonic(), deadline)
                    time.sleep(.01)
                child_pid = int(ready.read_text())
                if leader_exits_first:
                    leader.wait(timeout=2)
                MODULE.stop_process(leader, grace_seconds=.1)
                # An orphan may remain a zombie until init reaps it; it must
                # have stopped executing. We do reap the supervisor's own child.
                child_stat = Path(f"/proc/{child_pid}/stat")
                deadline = time.monotonic() + 2
                while child_stat.exists() and child_stat.read_text().split()[2] != "Z":
                    self.assertLess(time.monotonic(), deadline, "descendant still running")
                    time.sleep(.01)
                self.assertIsNotNone(leader.poll())
            finally:
                try:
                    os.killpg(leader.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                leader.wait(timeout=2)

    def test_child_stops_after_leader_has_exited(self):
        self.check_descendant_cleanup(leader_exits_first=True)

    def test_child_stops_when_leader_exits_on_term(self):
        self.check_descendant_cleanup(leader_exits_first=False)


if __name__ == "__main__":
    unittest.main()
