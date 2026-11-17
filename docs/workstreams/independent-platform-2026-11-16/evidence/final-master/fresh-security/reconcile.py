#!/usr/bin/env python3
"""Read preserved CI artifacts; do not download, scan, build, or change source."""
import argparse
import base64
from collections import Counter
from datetime import datetime, timezone
import gzip
import hashlib
import json
from pathlib import Path
import re
import subprocess
import warnings
import zipfile

REVISION = "89e883fc700ef0250024427a1f09bb88affe0383"
CHECKOUT = "b5c17b6f989bb11575e83544d37cc37fc6385d19"
BASELINE = "ba838872d72993abf1953dcafc64ba043f978c06"
WORKFLOW = 43982303611


def digest(data):
    return hashlib.sha256(data).hexdigest()


def json_digest(value):
    return digest(json.dumps(value, sort_keys=True, separators=(",", ":")).encode())


def read_zip(path, member):
    with zipfile.ZipFile(path) as archive:
        return archive.read(member)


def read_json(path, member):
    return json.loads(read_zip(path, member))


def source_key(result):
    physical = result["locations"][0]["physicalLocation"]
    region = physical["region"]
    return (result["ruleId"], physical["artifactLocation"]["uri"],
            region["startLine"], region.get("startColumn"))


def resolved(value, run, parent=""):
    """Resolve SARIF references, retaining descriptors, properties, and locations.

    Numeric artifact indices are redundant with their present URI. Rule and
    dataflow-taxon indices are replaced by the referenced descriptor and named
    component/version; they are not simply discarded. All other result fields
    remain, including messages, fingerprints, regions, and ordered flow nodes.
    """
    if isinstance(value, list):
        return [resolved(item, run, parent) for item in value]
    if not isinstance(value, dict):
        return value
    if parent in ("rule", "taxa") and "index" in value:
        component_index = value.get("toolComponent", {}).get("index")
        component = (run["tool"]["driver"] if component_index is None
                     else run["tool"]["extensions"][component_index])
        descriptor = component["rules" if parent == "rule" else "taxa"][value["index"]]
        if "id" in value:
            assert value["id"] == descriptor["id"]
        return {
            "resolvedComponent": component["name"],
            "resolvedVersion": component.get("semanticVersion"),
            "descriptor": resolved(descriptor, run, "descriptor"),
            "referenceProperties": resolved(value.get("properties", {}), run, "properties"),
        }
    if parent == "artifactLocation" and "index" in value:
        assert "uri" in value, "An index-only artifact requires explicit resolution"
        artifact = run["artifacts"][value["index"]]["location"]
        assert artifact["uri"] == value["uri"]
    return {
        key: resolved(item, run, key)
        for key, item in value.items()
        if key != "ruleIndex" and not (key == "index" and parent == "artifactLocation")
    }


def locations(value):
    found = set()
    if isinstance(value, dict):
        if "artifactLocation" in value:
            uri = value["artifactLocation"].get("uri")
            if uri:
                found.add(uri)
        for item in value.values():
            found.update(locations(item))
    elif isinstance(value, list):
        for item in value:
            found.update(locations(item))
    return found


def severity(score):
    value = float(score)
    return "CRITICAL" if value >= 9 else "HIGH" if value >= 7 else "MEDIUM" if value >= 4 else "LOW"


def git(repo, *args):
    return subprocess.check_output(["git", "-C", str(repo), *args]).decode().strip()


def tree_blobs(repo, revision):
    entries = git(repo, "ls-tree", "-r", revision).splitlines()
    return {entry.split("\t", 1)[1]: entry.split("\t", 1)[0].split()[2] for entry in entries}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--canonical-evidence", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, default=Path(__file__).resolve().parent)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[6]
    current = args.canonical_evidence / "final-master/final-ci"
    previous = args.canonical_evidence / "final-integrated/independent/security"
    output = args.output_dir
    output.mkdir(parents=True, exist_ok=True)
    metadata = json.loads((current / "artifacts.json").read_text())
    manifests = []
    names = {
        "source-security-go.zip": "source-security-go-" + CHECKOUT,
        "source-security-js.zip": "source-security-javascript-typescript-" + CHECKOUT,
        "image-scan.zip": "pgbase-image-scan-sarif",
        "sbom.zip": "pgbase-sbom",
        "runtime-assets.zip": "runtime-assets-" + CHECKOUT,
    }
    for filename, name in names.items():
        item = next(item for item in metadata["artifacts"] if item["name"] == name)
        data = (current / "artifacts" / filename).read_bytes()
        assert digest(data) == item["digest"].removeprefix("sha256:")
        assert len(data) == item["size_in_bytes"]
        assert item["workflow_run"]["id"] == WORKFLOW
        assert item["workflow_run"]["head_sha"] == REVISION
        with zipfile.ZipFile(current / "artifacts" / filename) as archive:
            members = {name: {"bytes": len(archive.read(name)), "sha256": digest(archive.read(name))}
                       for name in archive.namelist()}
        manifests.append({"canonical_path": "final-master/final-ci/artifacts/" + filename,
                          "artifact_id": item["id"], "name": name,
                          "bytes": len(data), "sha256": digest(data),
                          "api_digest_and_size_match": True, "members": members})

    source_rows, paths, source_reports, coverage = [], set(), [], {}
    for oldfile, newfile, member in [
        ("go.zip", "source-security-go.zip", "go.sarif"),
        ("js.zip", "source-security-js.zip", "javascript.sarif"),
    ]:
        old = read_json(previous / oldfile, member)["runs"][0]
        new = read_json(current / "artifacts" / newfile, member)["runs"][0]
        old_results = {source_key(result): (i, result) for i, result in enumerate(old["results"], 1)}
        new_keys = [source_key(result) for result in new["results"]]
        assert set(old_results) == set(new_keys)
        assert len(old_results) == len(old["results"]) == len(new_keys)
        summary = read_json(current / "artifacts" / newfile, "summary.json")
        assert summary["sourceCommit"] == CHECKOUT and int(summary["workflowRun"]) == WORKFLOW
        extracted = set()
        notification_levels = Counter()
        for invocation in new.get("invocations", []):
            assert invocation.get("executionSuccessful") is True
            for notification in invocation.get("toolExecutionNotifications", []):
                notification_levels[notification.get("level", "unspecified")] += 1
                if notification.get("descriptor", {}).get("id", "").endswith("/successfully-extracted-files"):
                    extracted.update(locations(notification.get("locations", [])))
        coverage[member] = sorted(extracted)
        source_reports.append({"member": member, "fresh_summary": summary,
                               "prior_summary": read_json(previous / oldfile, "summary.json"),
                               "result_order_equal": list(old_results) == new_keys,
                               "notification_levels": dict(notification_levels),
                               "extracted_path_count": len(extracted)})
        for fresh_index, result in enumerate(new["results"], 1):
            prior_index, prior = old_results[source_key(result)]
            canon, old_canon = resolved(result, new), resolved(prior, old)
            assert canon == old_canon, source_key(result)
            score = canon["rule"]["descriptor"]["properties"]["security-severity"]
            result_paths = sorted(locations(result))
            paths.update(result_paths)
            flows = result.get("codeFlows", [])
            source_rows.append({
                "member": member, "fresh_input_index": fresh_index, "prior_input_index": prior_index,
                "identity": list(source_key(result)), "security_score": score,
                "security_severity": severity(score),
                "sarif_level": result.get("level", canon["rule"]["descriptor"].get("defaultConfiguration", {}).get("level", "warning")),
                "message": result["message"]["text"], "partial_fingerprints": result.get("partialFingerprints", {}),
                "raw_prior_result_sha256": json_digest(prior), "raw_fresh_result_sha256": json_digest(result),
                "resolved_result_sha256": json_digest(canon), "resolved_results_equal": True,
                "resolved_dataflows_equal": resolved(flows, new) == resolved(prior.get("codeFlows", []), old),
                "codeflow_count": len(flows),
                "thread_node_count": sum(len(thread["locations"]) for flow in flows for thread in flow["threadFlows"]),
                "repository_paths": result_paths,
            })

    prior_blobs, fresh_blobs = tree_blobs(repo, BASELINE), tree_blobs(repo, REVISION)
    blob_rows = []
    for path in sorted(paths):
        assert path in prior_blobs and path in fresh_blobs
        assert prior_blobs[path] == fresh_blobs[path]
        data = subprocess.check_output(["git", "-C", str(repo), "show", REVISION + ":" + path])
        blob_rows.append({"path": path, "prior_git_blob": prior_blobs[path],
                          "fresh_git_blob": fresh_blobs[path], "same_blob": True,
                          "fresh_bytes_sha256": digest(data)})

    image_rows = []
    for member in ["pgbase-image.sarif", "pgbase-image-all-severities.sarif"]:
        old = read_json(previous / "image.zip", member)["runs"][0]
        new = read_json(current / "artifacts/image-scan.zip", member)["runs"][0]
        assert old.get("results", []) == new.get("results", [])
        assert old["tool"]["driver"]["rules"] == new["tool"]["driver"]["rules"]
        if member.endswith("all-severities.sarif"):
            for index, result in enumerate(new["results"], 1):
                rule = new["tool"]["driver"]["rules"][result["ruleIndex"]]
                fields = dict(line.split(": ", 1) for line in result["message"]["text"].splitlines() if ": " in line)
                image_rows.append({"fresh_input_index": index, "cve": result["ruleId"],
                                   "package": fields["Package"], "installed_version": fields["Installed Version"],
                                   "severity": fields["Severity"], "fixed_version": fields.get("Fixed Version", ""),
                                   "raw_result_sha256": json_digest(result), "raw_rule_sha256": json_digest(rule),
                                   "prior_result_and_rule_equal": True,
                                   "title": rule["shortDescription"]["text"],
                                   "reference": rule["helpUri"]})
        else:
            assert not new.get("results", [])

    runtime = read_json(current / "artifacts/runtime-assets.zip", "manifest.json")
    previous_runtime = read_json(previous / "runtime-assets.zip", "manifest.json")
    assert {k: v for k, v in runtime.items() if k != "imageID"} == {k: v for k, v in previous_runtime.items() if k != "imageID"}
    runtime_members = []
    with zipfile.ZipFile(current / "artifacts/runtime-assets.zip") as new, zipfile.ZipFile(previous / "runtime-assets.zip") as old:
        for name in new.namelist():
            runtime_members.append({"member": name, "bytes": len(new.read(name)),
                                    "sha256": digest(new.read(name)), "same_bytes_as_prior": new.read(name) == old.read(name)})
        cert_bytes = new.read("certs/ca-certificates.crt")
        blocks = re.findall(rb"-----BEGIN CERTIFICATE-----\s*(.*?)\s*-----END CERTIFICATE-----", cert_bytes, re.S)
        ders = [base64.b64decode(block) for block in blocks]
        assert len(ders) == runtime["certificateCount"]
        assert {digest(der) for der in ders} == set(runtime["certificatesByDER_SHA256"])
        assert digest(cert_bytes) == runtime["certificateFileSHA256"]["certs/ca-certificates.crt"]
        assert digest(new.read("tzdata.zi")) == runtime["timezoneSourceSHA256"]
        package_architectures = {}
        for name in new.namelist():
            if name.startswith("package-status/") and not name.endswith(".md5sums"):
                fields = dict(line.split(": ", 1) for line in new.read(name).decode().splitlines() if ": " in line)
                package_architectures[name.split("/")[1]] = fields.get("Architecture", "unspecified")
    certificate_anomalies, certificate_parse_warning_count = [], 0
    try:
        from cryptography import x509
        reference_time = datetime(2026, 11, 16, 23, 50, tzinfo=timezone.utc)
        with warnings.catch_warnings(record=True) as observed_warnings:
            warnings.simplefilter("always")
            for der in ders:
                cert = x509.load_der_x509_certificate(der)
                reasons = []
                if cert.not_valid_after_utc < reference_time:
                    reasons.append("expired_at_CI_time")
                if cert.serial_number <= 0:
                    reasons.append("nonpositive_serial")
                if reasons:
                    certificate_anomalies.append({"der_sha256": digest(der), "subject": cert.subject.rfc4514_string(),
                                                  "serial": str(cert.serial_number), "expires": cert.not_valid_after_utc.isoformat(),
                                                  "reasons": reasons})
            certificate_parse_warning_count = len(observed_warnings)
    except ImportError:
        certificate_anomalies = [{"status": "cryptography unavailable; DER identity only verified"}]
    sbom = read_json(current / "artifacts/sbom.zip", "pgbase-sbom")
    native = []
    for package in sbom["packages"]:
        if package["name"] in ["libc6", "libssl3", "postgresql-client-16", "postgresql-client-17", "postgresql-client-18"]:
            purls = [ref["referenceLocator"] for ref in package["externalRefs"] if ref["referenceType"] == "purl"]
            assert package_architectures[package["name"]] == "amd64"
            assert all("arch=amd64" in purl for purl in purls) and purls
            native.append({"name": package["name"], "version": package["versionInfo"], "purls": purls})

    log_rows = []
    for job in [133701569733, 133701569579, 133701569385]:
        path = current / "logs" / f"job-{job}.log.gz"
        packed = path.read_bytes()
        data = gzip.decompress(packed)
        lines = data.decode().splitlines()
        assert any(CHECKOUT in line for line in lines)
        evaluated = [tuple(map(int, match)) for match in re.findall(r"\[(\d+)/(\d+) eval[^\]]*\] Evaluation done", data.decode())]
        queries = re.findall(r'Interpreted (\w+) query .*\(([^()]*)\) at path', data.decode())
        tests_by_module = Counter(re.findall(r'Z test_\S+ \(([^.()]+)\.[^\n]*?\) \.\.\. ok', data.decode()))
        selected = [line for line in lines if (
            "HEAD is now at b5c17b6" in line or "No precomputed diff ranges found" in line
            or "No vulnerabilities found." in line or "found 0 vulnerabilities" in line
            or "Ran 17 tests" in line or "Analysis scope:" in line
            or "CodeQL CLI version" in line or "queries: security-extended" in line
            or "INPUT_IGNORE_UNFIXED: false" in line or "version: v0.70.0" in line
            or ("[37/37 eval" in line) or ("[105/105 eval" in line)
        )]
        if evaluated:
            total = max(total for _, total in evaluated)
            assert sorted({number for number, _ in evaluated}) == list(range(1, total + 1))
        log_rows.append({"job_id": job, "canonical_path": f"final-master/final-ci/logs/job-{job}.log.gz",
                         "gzip_sha256": digest(packed), "decoded_log_sha256": digest(data),
                         "decoded_log_bytes": len(data), "evaluated_query_count": max((n for _, n in evaluated), default=0),
                         "script_tests_by_module": dict(tests_by_module),
                         "interpreted_query_kinds": dict(Counter(kind for kind, _ in queries)),
                         "interpreted_query_ids": [query for _, query in queries], "selected_lines": selected})
    tree = git(repo, "rev-parse", REVISION + "^{tree}")
    assert tree == git(repo, "rev-parse", CHECKOUT + "^{tree}")
    excluded_go = sorted(path for path in fresh_blobs if path.endswith(".go") and not path.endswith("_test.go") and path not in coverage["go.sarif"])
    result = {
        "kind": "fresh-security-artifact-reconciliation/v1",
        "frozen_candidate": REVISION, "ci_checkout": CHECKOUT, "tree": tree,
        "workflow_run": WORKFLOW, "historical_workflow_run": 43861850923, "historical_checkout": BASELINE,
        "artifact_archives": manifests, "source_reports": source_reports,
        "source_counts_by_security_severity": dict(Counter(row["security_severity"] for row in source_rows)),
        "source_result_count": len(source_rows), "source_findings": source_rows,
        "all_source_results_equal_after_resolving_sarif_references": True,
        "source_path_count": len(blob_rows), "source_blobs": blob_rows,
        "all_source_result_paths_unchanged": True,
        "codeql_extracted_paths": coverage, "unextracted_non_test_go_paths": excluded_go,
        "image_high_critical_gate_results": 0, "image_findings": image_rows,
        "image_counts_by_severity": dict(Counter(row["severity"] for row in image_rows)),
        "image_raw_results_and_rules_equal_to_prior": True,
        "runtime": {"image_id": runtime["imageID"], "prior_image_id": previous_runtime["imageID"],
                    "manifest_equal_except_image_id": True, "members": runtime_members,
                    "certificate_count": len(ders), "certificate_der_set_verified": True,
                    "certificate_parse_warning_count": certificate_parse_warning_count,
                    "certificate_anomalies": certificate_anomalies,
                    "trust_environment": runtime["trustEnvironment"],
                    "timezone_version": runtime["timezoneVersionLine"], "native_sbom_packages": native,
                    "package_architectures": package_architectures},
        "logs": log_rows,
        "limits": ["Static artifact and source triage only; no new scans or tests were run by this script.",
                   "Full configured CodeQL scope is not every platform, language, test, or native dependency.",
                   "Native-helper vulnerable-function reachability and all image/SBOM file bytes are not established.",
                   "Certificate inventory identity does not certify TLS behavior or every trust anchor."]
    }
    (output / "reconciliation.json").write_text(json.dumps(result, indent=2) + "\n")
    print(json.dumps({"source": result["source_counts_by_security_severity"], "source_paths": len(blob_rows),
                      "image": result["image_counts_by_severity"], "source_results_equal": True,
                      "certificate_anomalies": len(certificate_anomalies), "excluded_non_test_go": len(excluded_go)}))


if __name__ == "__main__":
    main()
