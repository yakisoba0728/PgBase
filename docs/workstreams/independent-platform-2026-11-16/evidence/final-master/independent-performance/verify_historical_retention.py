#!/usr/bin/env python3
"""Verify retained bytes of the historical failed gate without rerunning it."""

import argparse
import csv
import hashlib
import io
import json
from pathlib import Path
import zipfile


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("metadata", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    metadata = json.loads(args.metadata.read_text())["artifact"]
    digest = sha256(args.archive.read_bytes())
    with zipfile.ZipFile(args.archive) as archive:
        raw = archive.read("load-evidence/open-loop-samples.csv")
        rows = list(csv.DictReader(io.StringIO(raw.decode())))
        log = archive.read("open-loop-http.txt").decode()
        counts = {
            "rows": len(rows), "unique_indices": len({row["index"] for row in rows}),
            "attempted": sum(int(row["http_service_ns"]) > 0 for row in rows),
            "completed": sum(int(row["status"]) != 0 for row in rows),
            "dropped": sum(row["error"] == "arrival queue full" for row in rows),
            "errors": sum(bool(row["error"]) for row in rows),
            "unscheduled": sum(row["error"].startswith("arrival not scheduled:") for row in rows),
        }
        checks = {
            "zip_digest_equals_historical_record": digest == "d2411a4837ffd38a5fd3c2b67423240f07de4eed9ed0f5d4038af59ca628cf6e",
            "zip_digest_equals_api": metadata["digest"] == "sha256:" + digest,
            "zip_size_equals_api": args.archive.stat().st_size == metadata["size_in_bytes"],
            "zip_crc_valid": archive.testzip() is None,
            "raw_ids_complete_in_order": [int(row["index"]) for row in rows] == list(range(120000)),
            "raw_scheduled_200_rps": all(int(row["scheduled_offset_ns"]) == int(row["index"]) * 5000000 for row in rows),
            "preserved_failure_counts": counts == {"rows": 120000, "unique_indices": 120000, "attempted": 119453, "completed": 119453, "dropped": 547, "errors": 547, "unscheduled": 0},
            "failed_test_is_preserved": "--- FAIL: TestOpenLoopHTTPQualification" in log and "--- PASS: TestOpenLoopHTTPQualification" not in log,
            "raw_csv_digest_equals_previous_independent_check": sha256(raw) == "6022b6f8e720facbc6902dd1e199844197b4d3edfb46c04c41a34f61765fe711",
        }
        result = {
            "audit_verdict": "PASS" if all(checks.values()) else "FAIL",
            "historical_workload_verdict": "FAIL",
            "scope": "Offline retention and failure verification only; no workload rerun, data mutation or historical result reclassification.",
            "artifact": metadata, "zip_path": str(args.archive), "zip_sha256": digest,
            "metadata_sha256": sha256(args.metadata.read_bytes()),
            "raw_csv_sha256": sha256(raw), "counts": counts, "checks": checks,
            "members": {name: {"sha256": sha256(archive.read(name)), "bytes": len(archive.read(name))} for name in archive.namelist() if not name.endswith("/")},
        }
    args.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"verdict": result["audit_verdict"], "historical_workload_verdict": "FAIL", "counts": counts, "passed_checks": sum(checks.values()), "checks_total": len(checks), "zip_sha256": digest}, indent=2))
    return 0 if all(checks.values()) else 1


if __name__ == "__main__":
    raise SystemExit(main())
