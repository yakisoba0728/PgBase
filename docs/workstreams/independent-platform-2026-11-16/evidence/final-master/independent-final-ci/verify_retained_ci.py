#!/usr/bin/env python3
"""Read-only verification of retained final CI metadata and decoded job logs.

This does not run application tests or contact GitHub. The root coordinator's
retained run/jobs metadata must be final before this verifier can accept it.
"""

import argparse
import datetime
import gzip
import hashlib
import json
import re
from pathlib import Path


RUN = 43982303611
SOURCE = "89e883fc700ef0250024427a1f09bb88affe0383"
CHECKOUT = "b5c17b6f989bb11575e83544d37cc37fc6385d19"
TREE = "c9a4c21a136fe6f9de0d854d0f7453aedb6a5b55"
UPLOAD = "Run actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9"
CODEQL = "Run github/codeql-action/analyze@33d7b85da1a04171e211befe2c739303b9a4c066"
EXPECTED = {
    133701568842: "mixed-http",
    133701569311: "frontend / test",
    133701569351: "backend / macOS terminal",
    133701569373: "backend / Go unit",
    133701569385: "security / audit",
    133701569432: "browser-and-recovery / e2e",
    133701569579: "source-analysis / analyze (javascript-typescript, none)",
    133701569629: "backend / Windows self-update",
    133701569656: "backend / Observability artifacts",
    133701569733: "source-analysis / analyze (go, manual)",
    133701569741: "backend / PostgreSQL integration / 16.13",
    133701569796: "backend / PostgreSQL integration / 18.3",
    133701569828: "backend / PostgreSQL integration / 17.9",
    133701569883: "backend / Release phase artifacts",
    133701570002: "backend / PostgreSQL integration / 17.9 / pgbase",
}
REQUIRED = {
    133701568842: [
        "Measure mixed HTTP load and pool recovery",
        "Qualify sustained fixed-arrival HTTP load", UPLOAD,
    ],
    133701569311: [
        "Verify browser example security boundaries", "Typecheck", "Test",
        "Rebuild the embedded administrator UI from an empty directory",
        "Verify the committed embedded output reproduces exactly",
    ],
    133701569351: [
        "Vet the darwin build",
        "Test the darwin terminal adapter with the race detector",
    ],
    133701569373: [
        "Verify source-security evidence tooling", "Verify formatting",
        "Run pinned blocking static analysis", "Vet unit build",
        "Test unit suite with skip policy",
        "Install the pinned private attestation verifier",
        "Verify private attestations against a disposable registry",
    ],
    133701569385: [
        "Scan reachable Go vulnerabilities", "Audit pinned frontend dependencies",
        "Build the production image in local CI mode", "Generate SPDX SBOM",
        "Record final runtime trust and timezone data", "Upload runtime data evidence",
        "Record image findings as SARIF", "Record every severity as SARIF",
        "Upload image scan SARIF", "Scan production image",
    ],
    133701569432: [
        "Verify Compose shutdown policy", "Build image in local CI mode",
        "Start isolated PostgreSQL project", "Start the authenticated browser backend",
        "Start the managed-backup browser backend", "Create the isolated browser administrators",
        "Verify browser onboarding and authenticated workflows",
        "Verify Firefox and WebKit representative workflows",
        "Verify hardened runtime", "Run black-box tests", "Remove isolated Compose resources",
    ],
    133701569629: ["Test native Windows self-update and CLI integration"],
    133701569656: [
        "Validate Grafana dashboard JSON", "Validate Prometheus alert rules",
        "Validate dashboard PromQL", "Validate dashboard and alert metric contract",
    ],
    133701569883: [
        "Build immutable release phases", "Run the process rollout against the built artifacts",
        "Pack immutable release phase artifacts", "Upload immutable release phase artifacts",
    ],
}
for job_id in (133701569579, 133701569733):
    REQUIRED[job_id] = [
        CODEQL, "Verify the SARIF evidence parser",
        "Preserve findings and resolved rule metadata for classification", UPLOAD,
    ]
REQUIRED[133701569733].append("Build Go runtime packages for extraction")
for job_id in (133701569741, 133701569796, 133701569828, 133701570002):
    REQUIRED[job_id] = [
        "Provision matching PostgreSQL client tools", "Start local S3 integration fixture",
        "Test PostgreSQL integration suite with skip policy", "Stop local S3 integration fixture",
    ]
    if job_id != 133701570002:
        REQUIRED[job_id] += [
            "Analyze and test Dart SDK", "Install and verify TypeScript SDK against PgBase",
            "Verify browser SDK example modules",
            "Verify the TypeScript SDK distribution reproduces exactly",
            "Record benchmark baseline", "Upload benchmark baseline",
        ]
REQUIRED[133701569828] += [
    "Vet integration build", "Test bounded multi-process durability",
    "Test administrator SDKs against a real server",
]


def digest(data):
    return hashlib.sha256(data).hexdigest()


def checkout_ids(text):
    found = []
    lines = text.splitlines()
    for index, line in enumerate(lines):
        # Covers Unix git and the quoted Windows git.exe command.
        if "[command]" in line and "log -1 --format=%H" in line:
            for following in lines[index + 1:index + 4]:
                match = re.search(r"\b[0-9a-f]{40}\b", following)
                if match:
                    found.append(match.group())
                    break
    return found


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("retained_ci", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    errors = []
    gate_failures = []
    pending = []

    def require(condition, message):
        if not condition:
            errors.append(message)

    def gate(status, conclusion, message):
        if status != "completed":
            pending.append(message + ": " + status)
        elif conclusion != "success":
            gate_failures.append(message + ": " + str(conclusion))

    metadata = {}
    hashes = {}
    for filename in ("run.json", "jobs.json", "log-manifest.json", "artifacts.json", "source-provenance.json"):
        raw = (args.retained_ci / filename).read_bytes()
        metadata[filename] = json.loads(raw)
        hashes[filename] = {"bytes": len(raw), "sha256": digest(raw)}
    run = metadata["run.json"]
    jobs = metadata["jobs.json"]
    require(run["id"] == RUN, "Unexpected workflow run")
    require(run["head_sha"] == SOURCE, "Unexpected workflow source")
    require(run["run_attempt"] == 1, "Unexpected workflow attempt")
    gate(run["status"], run["conclusion"], "Workflow")
    require(jobs["total_count"] == 15 and len(jobs["jobs"]) == 15, "Expected exactly 15 actual jobs")
    require({j["id"]: j["name"] for j in jobs["jobs"]} == EXPECTED, "Job ID/name set differs from the full qualification matrix")
    provenance = metadata["source-provenance.json"]
    require(provenance["source_sha"] == SOURCE and provenance["tree"] == TREE, "Retained source provenance differs")

    manifest = metadata["log-manifest.json"]
    require(manifest["run_id"] == RUN and manifest["source_sha"] == SOURCE and manifest["checkout_sha"] == CHECKOUT, "Log manifest source/run/checkout differs")
    logs = {item["job_id"]: item for item in manifest["records"]}
    require(set(logs) == set(EXPECTED), "Missing or extra retained job log")
    require(len(manifest["records"]) == 15, "Repeated retained job log record")
    records = []
    for job in jobs["jobs"]:
        job_id = job["id"]
        gate(job["status"], job["conclusion"], f"Job {job_id}")
        require(job["run_id"] == RUN and job["head_sha"] == SOURCE, f"Job {job_id} metadata has a different run/source")
        steps = {s["name"]: s for s in job["steps"]}
        required_results = []
        for step in REQUIRED[job_id]:
            actual = steps.get(step, {})
            status = actual.get("conclusion", "missing")
            required_results.append({"name": step, "conclusion": status})
            gate(actual.get("status", "completed"), status, f"Job {job_id} required step {step!r}")
        for step in job["steps"]:
            if step["conclusion"] not in ("success", "skipped"):
                gate(step["status"], step["conclusion"], f"Job {job_id} step {step['name']!r}")
        entry = logs.get(job_id)
        record = {
            "job_id": job_id, "name": job["name"], "conclusion": job["conclusion"],
            "started_at": job["started_at"], "completed_at": job["completed_at"],
            "required_steps": required_results,
            "skipped_steps": [s["name"] for s in job["steps"] if s["conclusion"] == "skipped"],
        }
        if entry:
            filename = "logs/" + Path(entry["path"]).name
            compressed = (args.retained_ci / filename).read_bytes()
            raw = gzip.decompress(compressed)
            checkouts = checkout_ids(raw.decode("utf-8"))
            require(digest(compressed) == entry["stored_sha256"], f"Job {job_id} compressed hash mismatch")
            require(digest(raw) == entry["utf8_sha256"], f"Job {job_id} decoded hash mismatch")
            require(len(raw) == entry["utf8_bytes"] and len(compressed) == entry["stored_bytes"], f"Job {job_id} byte count mismatch")
            require(checkouts == [CHECKOUT], f"Job {job_id} actual checkout mismatch: {checkouts}")
            record.update({
                "log": filename, "stored_bytes": len(compressed), "stored_sha256": digest(compressed),
                "decoded_utf8_bytes": len(raw), "decoded_utf8_sha256": digest(raw), "actual_checkouts": checkouts,
            })
        records.append(record)
    report = {
        "observed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "workflow_run": RUN, "frozen_source": SOURCE, "actual_checkout": CHECKOUT, "tree": TREE,
        "evidence_integrity": "PASS" if not errors else "INCOMPLETE_OR_FAILED",
        "qualification": "PASS" if not errors and not gate_failures and not pending else "NOT_READY_BLOCKED",
        "errors": errors, "gate_failures": sorted(set(gate_failures)), "pending": sorted(set(pending)),
        "metadata_hashes": hashes, "jobs": records,
        "required_successful_steps": sum(len(items) for items in REQUIRED.values()),
        "encoding_limit": "Logs are the connector-decoded response re-encoded as UTF-8 and losslessly gzipped. This verifies retained bytes, not an original HTTP ZIP representation.",
        "scope_limit": "Verifies metadata, required step outcomes, actual checkout and retained log integrity. Runtime coverage counts, source composition, security applicability and performance raw data are reviewed separately.",
    }
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({k: report[k] for k in ("evidence_integrity", "qualification", "errors", "gate_failures", "pending", "required_successful_steps")}))
    return 0 if report["qualification"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
