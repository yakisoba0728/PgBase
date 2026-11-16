#!/usr/bin/env python3
"""Reconcile exact-source security artifacts without running new scanners."""
from collections import Counter
import base64
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys
import zipfile


def git(repo, *args):
    return subprocess.check_output(["git", *args], cwd=repo, text=True).strip()


def read_zip(path, name):
    with zipfile.ZipFile(path) as archive:
        return archive.read(name)


def identity(result):
    return json.dumps({k: result.get(k) for k in ("ruleId", "message", "partialFingerprints")}, sort_keys=True)


def primary_locations(result, run):
    reference = result["rule"]
    component = run["tool"]["extensions"][reference["toolComponent"]["index"]]
    assert component["rules"][reference["index"]]["id"] == result["ruleId"] == reference["id"]
    locations = []
    for location in result["locations"]:
        physical = location["physicalLocation"]
        artifact = physical["artifactLocation"]
        resolved = run["artifacts"][artifact["index"]]["location"] if "index" in artifact else artifact
        uri = artifact.get("uri", resolved["uri"])
        assert uri == resolved["uri"]
        locations.append({"uri": uri, "region": physical.get("region", {})})
    return sorted(locations, key=lambda value: json.dumps(value, sort_keys=True))


def primary_location_differences(prior, fresh):
    before, after = {}, {}
    for run, groups in ((prior, before), (fresh, after)):
        for result in run["results"]:
            groups.setdefault(identity(result), []).append(primary_locations(result, run))
        for locations in groups.values():
            locations.sort(key=lambda value: json.dumps(value, sort_keys=True))
    return [{"identity": json.loads(key), "before": before[key], "after": after[key]} for key in sorted(set(before) & set(after)) if before[key] != after[key]]


def paths(value, run):
    result = set()
    if isinstance(value, dict):
        if "artifactLocation" in value:
            location = value["artifactLocation"]
            uri = location.get("uri")
            if uri is None and "index" in location:
                uri = run["artifacts"][location["index"]]["location"]["uri"]
            if uri:
                result.add(uri)
        for child in value.values():
            result.update(paths(child, run))
    elif isinstance(value, list):
        for child in value:
            result.update(paths(child, run))
    return result


def main(repo, previous, current, head, merge, workflow_run):
    tree = git(repo, "rev-parse", head + "^{tree}")
    assert tree == git(repo, "rev-parse", merge + "^{tree}")
    changed = git(repo, "diff", "--name-only", "a1a652289f0bc817f18db257282c758e66b4a672", head).splitlines()
    changed_go = [p for p in changed if p.endswith(".go") and not p.endswith("_test.go")]
    changed_js = [p for p in changed if p.endswith((".ts", ".tsx")) and "/src/" in p and not re.search(r"\.(test|spec)\.tsx?$", p)]
    result = {"head": head, "analyzed_merge": merge, "tree": tree, "workflow_run": int(workflow_run), "previous_workflow_run": 43820007603, "source": {}}
    reported = set()
    for label, filename, changed_paths in (("go", "go.sarif", changed_go), ("js", "javascript.sarif", changed_js)):
        prior = json.loads(read_zip(previous / (label + ".zip"), filename))["runs"][0]
        fresh = json.loads(read_zip(current / (label + ".zip"), filename))["runs"][0]
        summary = json.loads(read_zip(current / (label + ".zip"), "summary.json"))
        assert summary["sourceCommit"] == merge and summary["workflowRun"] == workflow_run
        assert summary["reports"][0]["sha256"] == hashlib.sha256(read_zip(current / (label + ".zip"), filename)).hexdigest()
        assert not summary["reports"][0]["diffRangeRestricted"]
        old_ids, new_ids = Counter(map(identity, prior["results"])), Counter(map(identity, fresh["results"]))
        reported.update(paths(prior["results"], prior) | paths(fresh["results"], fresh))
        extracted, notifications = set(), []
        for invocation in fresh["invocations"]:
            assert invocation["executionSuccessful"]
            for note in invocation.get("toolExecutionNotifications", []):
                if note.get("descriptor", {}).get("id", "").endswith("/diagnostics/successfully-extracted-files"):
                    extracted.update(paths(note, fresh))
                if note.get("level") in ("warning", "error"):
                    notifications.append(note)
        result["source"][label] = {
            "summary": summary, "result_count": len(fresh["results"]),
            "identities_equal": old_ids == new_ids,
            "new_identities": [json.loads(x) for x in (new_ids - old_ids).elements()],
            "removed_identities": [json.loads(x) for x in (old_ids - new_ids).elements()],
            "raw_result_records_equal": prior["results"] == fresh["results"],
            "result_identity_order_equal": list(map(identity, prior["results"])) == list(map(identity, fresh["results"])),
            "comparison_method": "resolve rule IDs; match rule/message/fingerprint identities and primary source URI/region; preserve raw reports without claiming codeflow or byte equivalence",
            "primary_location_differences": primary_location_differences(prior, fresh),
            "extracted_locations": len(extracted), "changed_runtime_paths": changed_paths,
            "changed_runtime_paths_missing_from_extraction": [p for p in changed_paths if p not in extracted],
            "warning_error_extraction_notifications": notifications,
        }
    result["reported_source_blob_comparison"] = {p: {"before": git(repo, "rev-parse", "a1a652289f0bc817f18db257282c758e66b4a672:" + p), "after": git(repo, "rev-parse", head + ":" + p)} for p in sorted(reported)}
    result["changed_reported_source_paths"] = [p for p, v in result["reported_source_blob_comparison"].items() if v["before"] != v["after"]]
    prior = json.loads(read_zip(previous / "image.zip", "pgbase-image-all-severities.sarif"))["runs"][0]
    fresh = json.loads(read_zip(current / "image.zip", "pgbase-image-all-severities.sarif"))["runs"][0]
    gate = json.loads(read_zip(current / "image.zip", "pgbase-image.sarif"))["runs"][0]
    result["image"] = {
        "result_count": len(fresh["results"]), "records_equal": prior["results"] == fresh["results"],
        "rule_metadata_equal": prior["tool"]["driver"]["rules"] == fresh["tool"]["driver"]["rules"],
        "high_critical_gate_results": len(gate.get("results", [])),
        "severity_counts": dict(Counter(re.search(r"Severity: (\w+)", r["message"]["text"])[1] for r in fresh["results"])),
    }
    with zipfile.ZipFile(current / "runtime-assets.zip") as archive:
        manifest = json.loads(archive.read("manifest.json"))
        bundle = archive.read("certs/ca-certificates.crt")
        fingerprints = [hashlib.sha256(base64.b64decode(x)).hexdigest() for x in re.findall(b"-----BEGIN CERTIFICATE-----\\s*(.*?)\\s*-----END CERTIFICATE-----", bundle, re.S)]
        timezone = archive.read(next(p for p in archive.namelist() if Path(p).name == "tzdata.zi"))
        statuses = {Path(p).name: archive.read(p).decode() for p in archive.namelist() if p.startswith("package-status/") and not p.endswith(".md5sums")}
        result["runtime_assets"] = {
            "image_id": manifest["imageID"], "certificate_count": len(fingerprints),
            "unique_certificate_count": len(set(fingerprints)),
            "certificate_count_matches_manifest": len(fingerprints) == manifest["certificateCount"],
            "certificate_manifest_matches": set(fingerprints) == set(manifest["certificatesByDER_SHA256"]),
            "certificate_bundle_sha256": hashlib.sha256(bundle).hexdigest(),
            "trust_environment": manifest["trustEnvironment"],
            "timezone_version": timezone.splitlines()[0].decode(), "timezone_sha256": hashlib.sha256(timezone).hexdigest(),
            "package_versions": {name: re.search(r"^Version: (.+)$", content, re.M)[1] for name, content in statuses.items()},
        }
        result["runtime_assets"]["package_versions_match_manifest"] = result["runtime_assets"]["package_versions"] == manifest["packageVersions"]
    with zipfile.ZipFile(current / "sbom.zip") as archive:
        sbom = json.loads(archive.read(archive.namelist()[0]))
        result["sbom"] = {k: len(sbom.get(k, [])) for k in ("packages", "files", "relationships")}
        package_versions = {p["name"]: p.get("versionInfo") for p in sbom["packages"]}
        result["sbom"]["captured_package_versions_match"] = all(package_versions.get(name) == version for name, version in result["runtime_assets"]["package_versions"].items())
        result["sbom"]["native_package_urls"] = {p["name"]: [r["referenceLocator"] for r in p.get("externalRefs", []) if r.get("referenceType") == "purl"] for p in sbom["packages"] if p["name"] in ("libc6", "libssl3")}
    result["artifact_sha256"] = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in current.glob("*.zip")}
    metadata = json.loads((current / "artifacts.json").read_text())
    result["artifact_digests_match_github_metadata"] = all("sha256:" + digest in {a["digest"] for a in metadata} for digest in result["artifact_sha256"].values())
    assert result["artifact_digests_match_github_metadata"]
    (current / "recheck.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"run": result["workflow_run"], "head": head, "source_counts": {k: v["result_count"] for k, v in result["source"].items()}, "changed_reported_source_paths": result["changed_reported_source_paths"], "image": result["image"]}))


if __name__ == "__main__":
    main(Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]), *sys.argv[4:])
