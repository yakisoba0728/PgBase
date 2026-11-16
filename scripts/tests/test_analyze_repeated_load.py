"""The registered HTTP comparison must reject changed or incomplete workloads."""

import importlib.util
from pathlib import Path
import tempfile
import unittest


SPEC = importlib.util.spec_from_file_location(
    "analyze_repeated_load", Path(__file__).resolve().parents[1] / "analyze_repeated_load.py"
)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)


def http_log(workers=(1, 8, 32, 4), quantile_requests=1000, pool_max=4):
    lines = []
    for worker in workers:
        lines.extend([
            f"kind=all requests={quantile_requests} p50=1ms p95=2ms p99=3ms",
            f"workers={worker} elapsed=1s requests=1000 rps=1000 unexpected_errors=0 "
            "error_rate=0 allocated_bytes_per_request=512 allocations_per_request=5 "
            f"gc_cycles=0 gc_pause=0s pool_max={pool_max} pool_wait_count=0 "
            "pool_wait=0s pool_canceled=0",
        ])
    return "\n".join(lines) + "\n"


class RepeatedLoadEvidenceTests(unittest.TestCase):
    def parse(self, text):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "mixed.log"
            path.write_text(text)
            return MODULE.read_http(path)

    def test_registered_stages_are_accepted(self):
        result = self.parse(http_log())
        self.assertEqual(list(result), ["http/c1", "http/c8", "http/c32", "http/c4"])
        self.assertEqual(result["http/c32"]["p99-ms"], 3)
        self.assertEqual(result["http/c4"]["samples"], 1000)

    def test_changed_concurrency_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "HTTP stages"):
            self.parse(http_log(workers=(2, 8, 32, 4)))

    def test_reordered_recovery_stage_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "HTTP stages"):
            self.parse(http_log(workers=(1, 4, 32, 8)))

    def test_partial_latency_population_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "latency samples"):
            self.parse(http_log(quantile_requests=999))

    def test_changed_pool_budget_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "pool"):
            self.parse(http_log(pool_max=8))


if __name__ == "__main__":
    unittest.main()
