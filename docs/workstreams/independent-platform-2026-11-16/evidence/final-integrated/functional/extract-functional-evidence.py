#!/usr/bin/env python3
"""Preserve retrieved final CI logs and derive source/step/count evidence.

Usage: python extract-functional-evidence.py PATH_TO_RETRIEVED_LOG_DIRECTORY
Run after jobs.json records terminal results. No network or tests are run here.
The hashes describe the connector's UTF-8 decoded/re-encoded log bytes, not a
GitHub log ZIP. Artifact digests are separately identified as upload receipts.
"""
from __future__ import annotations

import gzip
import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parent
RAW = Path(sys.argv[1])
HEAD = "4091c3be20ec8db177886a2a85550924fce37ff5"
CHECKOUT = "ba838872d72993abf1953dcafc64ba043f978c06"
TREE = "653a46758f9e6af9662b77ecfe2f069a2ac94cb4"
RUN = 43861850923
jobs = json.loads((ROOT / "jobs.json").read_text())["jobs"]
functional_jobs = [job for job in jobs if job["name"] != "mixed-http"]
assert len(functional_jobs) == 14


def clean(line: str) -> str:
    line = re.sub(r"\x1b\[[0-9;]*[mK]", "", line)
    return re.sub(r"^\ufeff?\d{4}-\S+Z ", "", line)


def required_steps(name: str) -> list[str]:
    if name == "backend / Go unit":
        return ["Verify source-security evidence tooling", "Verify formatting",
                "Run pinned blocking static analysis", "Vet unit build",
                "Test unit suite with skip policy",
                "Verify private attestations against a disposable registry"]
    if "PostgreSQL integration" in name:
        result = ["Provision matching PostgreSQL client tools",
                  "Start local S3 integration fixture",
                  "Test PostgreSQL integration suite with skip policy"]
        if not name.endswith(" / pgbase"):
            result += ["Analyze and test Dart SDK",
                       "Install and verify TypeScript SDK against PgBase",
                       "Verify browser SDK example modules",
                       "Verify the TypeScript SDK distribution reproduces exactly"]
        if name.endswith(" / 17.9"):
            result += ["Vet integration build", "Test bounded multi-process durability",
                       "Test administrator SDKs against a real server"]
        return result
    if name == "frontend / test":
        return ["Verify browser example security boundaries", "Typecheck", "Test",
                "Rebuild the embedded administrator UI from an empty directory",
                "Verify the committed embedded output reproduces exactly"]
    if name == "browser-and-recovery / e2e":
        return ["Verify Compose shutdown policy",
                "Verify browser onboarding and authenticated workflows",
                "Verify Firefox and WebKit representative workflows",
                "Verify hardened runtime", "Run black-box tests"]
    if name == "security / audit":
        return ["Scan reachable Go vulnerabilities", "Audit pinned frontend dependencies",
                "Build the production image in local CI mode", "Generate SPDX SBOM",
                "Record final runtime trust and timezone data",
                "Record image findings as SARIF", "Record every severity as SARIF",
                "Scan production image"]
    if name.startswith("source-analysis"):
        result = ["Verify the SARIF evidence parser",
                  "Preserve findings and resolved rule metadata for classification"]
        if "(go," in name:
            result += ["Build Go runtime packages for extraction"]
        return result
    if name == "backend / Release phase artifacts":
        return ["Build immutable release phases",
                "Run the process rollout against the built artifacts",
                "Upload immutable release phase artifacts"]
    if name == "backend / Observability artifacts":
        return ["Validate Grafana dashboard JSON", "Validate Prometheus alert rules",
                "Validate dashboard PromQL", "Validate dashboard and alert metric contract"]
    if name == "backend / macOS terminal":
        return ["Vet the darwin build",
                "Test the darwin terminal adapter with the race detector"]
    if name == "backend / Windows self-update":
        return ["Test native Windows self-update and CLI integration"]
    raise ValueError(name)


results = []
manifest = []
excerpts = []
(ROOT / "logs").mkdir(exist_ok=True)
for job in functional_jobs:
    jid, name = job["id"], job["name"]
    data = (RAW / f"job-{jid}.log").read_bytes()
    zipped = gzip.compress(data, compresslevel=9, mtime=0)
    output = ROOT / "logs" / f"job-{jid}.log.gz"
    output.write_bytes(zipped)
    assert gzip.decompress(zipped) == data
    lines = data.decode("utf-8").splitlines()
    plain = [clean(line) for line in lines]
    checkout_lines = [i + 1 for i, line in enumerate(plain)
                      if line.strip() == CHECKOUT]
    assert checkout_lines, f"missing checkout evidence in {jid}"
    steps = {step["name"]: step for step in job["steps"]}
    required = required_steps(name)
    missing = [step for step in required if step not in steps or
               steps[step]["conclusion"] != "success"]
    assert not missing, f"required steps did not succeed in {jid}: {missing}"
    assert job["status"] == "completed" and job["conclusion"] == "success", jid
    assert not any(step["conclusion"] in ("failure", "cancelled", "timed_out")
                   for step in job["steps"]), jid
    package_pattern = r"^ok\s+(github\.com/yakisoba0728/PgBase/\S+)"
    package_passes = [match.group(1) for line in plain
                      if (match := re.search(package_pattern, line))]
    primary = None
    marker = ("scripts/test-go-unit.sh" if name == "backend / Go unit"
              else "scripts/test-go-integration.sh" if "PostgreSQL integration" in name
              else None)
    if marker:
        start = next(i for i, line in enumerate(plain) if marker in line)
        end = next((i for i in range(start + 1, len(plain))
                    if "##[group]" in plain[i]), len(plain))
        primary_packages = [match.group(1) for line in plain[start:end]
                            if (match := re.search(package_pattern, line))]
        primary = {"first_log_line": start + 1, "end_log_line_exclusive": end + 1,
                   "package_pass_summaries": len(primary_packages),
                   "packages": primary_packages,
                   "successful_individual_test_count": None,
                   "count_limit": "Existing testskip output suppresses successful individual test events."}
    top = [line for line in plain if line.startswith("--- PASS: ")]
    sub = [line for line in plain if re.match(r"^ +--- PASS: ", line)]
    snippets = [{"line": i + 1, "text": line} for i, line in enumerate(plain)
                if re.search(r"^ok\s+github\.com/yakisoba0728/PgBase/|"
                             r"^\s*\d+ passed \(|^\s*(Tests|Test Files)\s+\d+ passed|"
                             r"^ℹ (tests|pass|fail|skipped|cancelled) |"
                             r"^🎉 \d+ tests passed|^No issues found!|"
                             r"^\s*--- (PASS|FAIL|SKIP):|^SHA256 digest of uploaded artifact|"
                             r"^Artifact download URL:", line)]
    results.append({"job_id": jid, "name": name, "status": job["status"],
                    "conclusion": job["conclusion"],
                    "url": f"https://github.com/yakisoba0728/PgBase/actions/runs/{RUN}/job/{jid}",
                    "actual_checkout": CHECKOUT, "checkout_log_lines": checkout_lines,
                    "required_steps_success": required,
                    "conditional_steps_skipped": [step["name"] for step in job["steps"]
                                                  if step["conclusion"] == "skipped"],
                    "primary_go_suite": primary,
                    "all_go_package_pass_summaries": len(package_passes),
                    "verbose_top_level_passes": len(top),
                    "verbose_subtest_passes": len(sub),
                    "named_test_skips_reported": sum(bool(re.match(r"^\s*--- SKIP:", x)) for x in plain),
                    "named_test_failures_reported": sum(bool(re.match(r"^\s*--- FAIL:", x)) for x in plain)})
    manifest.append({"job_id": jid, "name": name,
                     "log_api": f"https://api.github.com/repos/yakisoba0728/PgBase/actions/jobs/{jid}/logs",
                     "file": str(output.relative_to(ROOT)),
                     "raw_utf8_bytes": len(data), "gzip_bytes": len(zipped),
                     "raw_utf8_sha256": hashlib.sha256(data).hexdigest(),
                     "gzip_sha256": hashlib.sha256(zipped).hexdigest(),
                     "gzip_mtime": 0, "decompression_equals_retrieved_utf8": True})
    excerpts.append({"job_id": jid, "name": name, "lines": snippets})

result = {"run_id": RUN, "run_attempt": 1, "candidate_head": HEAD,
          "actual_checkout": CHECKOUT, "candidate_and_checkout_tree": TREE,
          "functional_jobs_expected": 14, "functional_jobs_success": len(results),
          "required_step_failures": 0, "jobs": results,
          "performance_result_owned_by": "performance/final CI evidence",
          "scanner_finding_reconciliation_owned_by": "independent/final security evidence",
          "conditional_skip_note": "Release-image mode, cache hit/miss, failure-only artifact uploads and matrix shard/version exclusions remain unchanged; required functional coverage is asserted above."}
for filename, value in [("functional-result.json", result),
                        ("raw-log-manifest.json", manifest),
                        ("functional-log-excerpts.json", excerpts)]:
    (ROOT / filename).write_text(json.dumps(value, indent=2, ensure_ascii=False) + "\n")
print(json.dumps({"functional_jobs": len(results), "gzip_bytes": sum(x["gzip_bytes"] for x in manifest),
                  "raw_log_bytes": sum(x["raw_utf8_bytes"] for x in manifest)}))
