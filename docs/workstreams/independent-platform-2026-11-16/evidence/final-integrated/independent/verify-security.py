#!/usr/bin/env python3
"""Reconcile downloaded final scan artifacts; does not launch a scanner."""

import argparse
import base64
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import runpy
import subprocess
import zipfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--fresh", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    options = parser.parse_args()
    repo = options.repo
    workstream = repo / "docs/workstreams/independent-platform-2026-11-16"
    baseline = workstream / "evidence/phase-nonperformance-security-ci/ci-43836410887"
    # Reviewed helpers resolve indexed SARIF rule/artifact references before
    # comparison. The old script's hardcoded main/source/run mapping is not used.
    helpers = runpy.run_path(str(workstream / "evidence/phase-nonperformance-security-ci/reconcile.py"))
    paths = helpers["paths"]
    location_differences = helpers["primary_location_differences"]
    git = lambda *args: subprocess.check_output(["git", *args], cwd=repo, text=True).strip()
    read = lambda directory, label, name: zipfile.ZipFile(directory / (label + ".zip")).read(name)
    old_head = "56a881f47d0c27877b91959d7df641c2f603ef03"
    head = "4091c3be20ec8db177886a2a85550924fce37ff5"
    checkout = "ba838872d72993abf1953dcafc64ba043f978c06"
    assert git("rev-parse", head + "^{tree}") == git("rev-parse", checkout + "^{tree}")
    changed = git("diff", "--name-only", old_head, head).splitlines()
    identity = lambda r: json.dumps({k: r.get(k) for k in ["ruleId", "message", "partialFingerprints"]}, sort_keys=True)
    result = {"run_id": 43861850923, "head": head, "checkout": checkout, "tree": git("rev-parse", head + "^{tree}"), "baseline_run_id": 43836410887, "baseline_head": old_head, "source": {}}
    reported = set()
    for label, filename in [("go", "go.sarif"), ("js", "javascript.sarif")]:
        old = json.loads(read(baseline, label, filename))["runs"][0]
        raw = read(options.fresh, label, filename)
        fresh = json.loads(raw)["runs"][0]
        summary = json.loads(read(options.fresh, label, "summary.json"))
        assert summary["sourceCommit"] == checkout and str(summary["workflowRun"]) == "43861850923"
        assert summary["reports"][0]["sha256"] == hashlib.sha256(raw).hexdigest()
        assert not summary["reports"][0]["diffRangeRestricted"]
        before_ids, after_ids = Counter(map(identity, old["results"])), Counter(map(identity, fresh["results"]))
        extracted, notifications = set(), []
        for invocation in fresh["invocations"]:
            assert invocation["executionSuccessful"]
            for note in invocation.get("toolExecutionNotifications", []):
                if note.get("descriptor", {}).get("id", "").endswith("/diagnostics/successfully-extracted-files"):
                    extracted.update(paths(note, fresh))
                if note.get("level") in ("warning", "error"):
                    notifications.append(note)
        changed_runtime = [p for p in changed if (label == "go" and p.endswith(".go") and not p.endswith("_test.go")) or (label == "js" and p.endswith((".ts", ".tsx")) and "/src/" in p and not re.search(r"\.(test|spec)\.tsx?$", p))]
        reported.update(paths(old["results"], old) | paths(fresh["results"], fresh))
        result["source"][label] = {"results": len(fresh["results"]), "identities_equal": before_ids == after_ids, "new_identities": [json.loads(x) for x in (after_ids - before_ids).elements()], "removed_identities": [json.loads(x) for x in (before_ids - after_ids).elements()], "primary_location_differences": location_differences(old, fresh), "extracted_locations": len(extracted), "changed_runtime_paths": changed_runtime, "missing_changed_runtime_extractions": [p for p in changed_runtime if p not in extracted], "warning_error_extraction_notifications": notifications, "summary": summary}
    result["changed_reported_flow_sources"] = {p: {"old_blob": git("rev-parse", old_head + ":" + p), "new_blob": git("rev-parse", head + ":" + p)} for p in sorted(reported) if git("rev-parse", old_head + ":" + p) != git("rev-parse", head + ":" + p)}
    old = json.loads(read(baseline, "image", "pgbase-image-all-severities.sarif"))["runs"][0]
    fresh = json.loads(read(options.fresh, "image", "pgbase-image-all-severities.sarif"))["runs"][0]
    high = json.loads(read(options.fresh, "image", "pgbase-image.sarif"))["runs"][0]
    result["image"] = {"all_severity_results": len(fresh["results"]), "high_critical_results": len(high.get("results", [])), "all_result_records_equal": old["results"] == fresh["results"], "rule_metadata_equal": old["tool"]["driver"]["rules"] == fresh["tool"]["driver"]["rules"], "severity_counts": dict(Counter(re.search(r"Severity: (\w+)", r["message"]["text"])[1] for r in fresh["results"]))}
    with zipfile.ZipFile(options.fresh / "runtime-assets.zip") as z:
        manifest = json.loads(z.read("manifest.json"))
        bundle = z.read("certs/ca-certificates.crt")
        fingerprints = [hashlib.sha256(base64.b64decode(x)).hexdigest() for x in re.findall(b"-----BEGIN CERTIFICATE-----\\s*(.*?)\\s*-----END CERTIFICATE-----", bundle, re.S)]
        timezone = z.read(next(name for name in z.namelist() if Path(name).name == "tzdata.zi"))
        versions = {Path(name).name: re.search(r"^Version: (.+)$", z.read(name).decode(), re.M)[1] for name in z.namelist() if name.startswith("package-status/") and not name.endswith(".md5sums")}
        result["runtime_assets"] = {"image_id": manifest["imageID"], "certificate_count": len(fingerprints), "unique_certificates": len(set(fingerprints)), "certificate_manifest_matches": len(fingerprints) == manifest["certificateCount"] and set(fingerprints) == set(manifest["certificatesByDER_SHA256"]), "trust_bundle_sha256": hashlib.sha256(bundle).hexdigest(), "trust_environment": manifest["trustEnvironment"], "timezone_version": timezone.splitlines()[0].decode(), "timezone_sha256": hashlib.sha256(timezone).hexdigest(), "package_versions": versions, "package_versions_match_manifest": versions == manifest["packageVersions"]}
    with zipfile.ZipFile(options.fresh / "sbom.zip") as z:
        sbom = json.loads(z.read(z.namelist()[0]))
        inventory = {p["name"]: p.get("versionInfo") for p in sbom["packages"]}
        result["sbom"] = {"package_count": len(sbom["packages"]), "file_count": len(sbom.get("files", [])), "relationship_count": len(sbom.get("relationships", [])), "captured_versions_match": all(inventory.get(p) == version for p, version in versions.items()), "native_package_urls": {p["name"]: [r["referenceLocator"] for r in p.get("externalRefs", []) if r.get("referenceType") == "purl"] for p in sbom["packages"] if p["name"] in ("libc6", "libssl3")}}
    metadata = json.loads((options.fresh / "artifacts.json").read_text())
    result["artifacts"] = []
    for file in sorted(options.fresh.glob("*.zip")):
        digest = hashlib.sha256(file.read_bytes()).hexdigest()
        matched = [a for a in metadata if a.get("digest") == "sha256:" + digest]
        assert len(matched) == 1 and matched[0]["size_in_bytes"] == file.stat().st_size
        result["artifacts"].append({"file": file.name, "id": matched[0]["id"], "sha256": digest, "bytes": file.stat().st_size, "url": "https://github.com/yakisoba0728/PgBase/actions/runs/43861850923/artifacts/" + str(matched[0]["id"]), "expires_at": matched[0]["expires_at"]})
    result["scope"] = "Fresh artifact/source reconciliation and retained prior finding classifications; no new exhaustive repository or native-library reachability audit. Successful jobs do not mean zero findings."
    options.output.write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"source": {k: {f: v[f] for f in ["results", "identities_equal", "missing_changed_runtime_extractions", "primary_location_differences"]} for k, v in result["source"].items()}, "image": result["image"], "changed_reported_flow_sources": result["changed_reported_flow_sources"], "artifact_digests_verified": len(result["artifacts"])}))


if __name__ == "__main__":
    main()
