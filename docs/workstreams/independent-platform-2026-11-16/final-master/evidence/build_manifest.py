#!/usr/bin/env python3
"""Catalog evidence without rewriting it; verify all 617 original byte streams."""

import argparse
import collections
import datetime
import hashlib
import json
from pathlib import Path
import subprocess


BASELINE = "242f80aea730ee483cc22798fb6450bdb3064081"
TESTED = "4091c3be20ec8db177886a2a85550924fce37ff5"
CHECKOUT = "ba838872d72993abf1953dcafc64ba043f978c06"
TESTED_TREE = "653a46758f9e6af9662b77ecfe2f069a2ac94cb4"
RUN = 43861850923
PREFIX = "docs/workstreams/independent-platform-2026-11-16/"
EVIDENCE = PREFIX + "evidence/"
MASTER = PREFIX + "final-master/"
OUTPUT = MASTER + "evidence/manifest.json"
ROOT = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())


def git(*args):
    return subprocess.check_output(["git", "-C", str(ROOT), *args])


def sha256(raw):
    return hashlib.sha256(raw).hexdigest()


def read_json(path):
    return json.loads((ROOT / path).read_text())


def tree_entries(revision):
    entries = {}
    for record in git("ls-tree", "-rz", revision).split(b"\0"):
        if not record:
            continue
        metadata, path = record.split(b"\t", 1)
        mode, kind, blob = metadata.decode().split()
        if kind == "blob" and "/evidence/" in path.decode():
            entries[path.decode()] = {"git_blob": blob, "mode": mode}
    return entries


def baseline_hashes(entries):
    paths = sorted(entries)
    result = subprocess.run(
        ["git", "-C", str(ROOT), "cat-file", "--batch"],
        input="".join(entries[p]["git_blob"] + "\n" for p in paths).encode(),
        stdout=subprocess.PIPE, check=True,
    ).stdout
    offset = 0
    for path in paths:
        end = result.index(b"\n", offset)
        blob, kind, size = result[offset:end].split()
        if kind != b"blob":
            raise ValueError("Expected Git blob: " + path)
        size = int(size)
        raw = result[end + 1:end + 1 + size]
        entries[path].update(bytes=size, sha256=sha256(raw))
        offset = end + 1 + size + 1
    return entries


def sidecars(path, tracked):
    found = []
    parent = Path(path).parent
    while parent.as_posix() not in (".", "docs"):
        for name in ("manifest.json", "provenance.json", "source-provenance.json",
                     "source-verification.json", "run.json", "SHA256SUMS"):
            candidate = (parent / name).as_posix()
            if candidate != OUTPUT and candidate in tracked and candidate != path:
                found.append(candidate)
        if len(found) >= 3:
            break
        parent = parent.parent
    return found[:4]


def exact_artifact_metadata(tracked):
    """Only inherit structured fields when path AND digest match a sidecar entry."""
    matches = {}
    for sidecar in sorted(tracked):
        if not sidecar.endswith("manifest.json") or sidecar == OUTPUT:
            continue
        try:
            data = read_json(sidecar)
        except (UnicodeError, ValueError):
            continue
        if not isinstance(data, dict) or not isinstance(data.get("artifacts"), list):
            continue
        for artifact in data["artifacts"]:
            if not isinstance(artifact, dict):
                continue
            path = artifact.get("path")
            digest = artifact.get("sha256")
            if path not in tracked or not digest or not (ROOT / path).is_file():
                continue
            if sha256((ROOT / path).read_bytes()) == digest:
                matches[path] = (sidecar, artifact)
    return matches


def build(status):
    baseline = baseline_hashes(tree_entries(BASELINE))
    if len(baseline) != 617:
        raise ValueError("Original evidence count changed: " + str(len(baseline)))
    tracked = {p.decode() for p in git("ls-files", "-z").split(b"\0") if p}
    canonical = {p for p in tracked if p.startswith(MASTER) and p.endswith(".md")}
    canonical.add(MASTER + "evidence/build_manifest.py")
    paths = sorted({p for p in tracked if "/evidence/" in p and p != OUTPUT} | canonical | set(baseline))
    metadata = exact_artifact_metadata(tracked)
    inventory = read_json(EVIDENCE + "final-master/initial-inventory.json")
    functional_logs = {
        EVIDENCE + "final-integrated/functional/" + row["file"]: row
        for row in read_json(EVIDENCE + "final-integrated/functional/raw-log-manifest.json")
    }
    artifact_index = read_json(EVIDENCE + "final-integrated/coordinator/artifact-index.json")["artifacts"]
    artifact_by_digest = {a["digest"].removeprefix("sha256:"): a for a in artifact_index}
    preferred = {
        EVIDENCE + "final-integrated/closeout-20261116/performance/artifact-metadata.json": EVIDENCE + "final-integrated/performance/final-gate/artifact-metadata.json",
        EVIDENCE + "final-integrated/closeout-20261116/source-ci/artifacts.json": EVIDENCE + "final-integrated/functional/artifact-api.json",
        EVIDENCE + "final-integrated/closeout-20261116/source-ci/jobs.json": EVIDENCE + "final-integrated/functional/jobs.json",
    }
    entries = []
    for path in paths:
        raw = (ROOT / path).read_bytes()
        digest = sha256(raw)
        historical = path in baseline
        original = baseline.get(path)
        if original and (digest != original["sha256"] or len(raw) != original["bytes"]):
            raise ValueError("Original evidence modified: " + path)
        classification = "CANONICAL" if path in canonical else ("HISTORICAL" if historical else "PRIMARY_EVIDENCE")
        scoped = any(path.startswith(EVIDENCE + prefix) for prefix in (
            "final-integrated/functional/", "final-integrated/performance/final-gate/",
            "final-integrated/closeout-20261116/performance/",
            "final-integrated/closeout-20261116/source-ci/",
            "final-integrated/independent/security/",
        ))
        if scoped:
            classification = "PRIMARY_EVIDENCE"
        if path in preferred:
            classification = "REDUNDANT"
        item = {
            "path": path, "classification": classification,
            "temporal_scope": "historical_before_final_master" if historical else "final_master_addition",
            "bytes": len(raw), "sha256": digest,
            "original_git_blob": original["git_blob"] if original else None,
            "preserved_from_source_sha": BASELINE if historical else None,
            "source_sha": TESTED if scoped else None,
            "source_git_tree": TESTED_TREE if scoped else None,
            "ci_checkout_sha": CHECKOUT if scoped else None,
            "run_id": RUN if scoped else None,
            "job_id": None, "command": None,
            "provenance_references": sidecars(path, tracked),
            "preservation_location": "Git:" + path,
            "expires_at": None,
            "removal_allowed": False,
            "classification_reason": "Retained source-scoped evidence; no success/failure outcome is inferred from classification.",
            "missing_metadata_reason": "Null execution fields are not independently mapped to this file; consult original sidecar/log. Git copy has no Actions expiry.",
        }
        if path in canonical:
            item["classification_reason"] = "Current review navigation or manifest verification; not a runtime test execution."
        if path in preferred:
            item["canonical_path"] = preferred[path]
            item["classification_reason"] = "Same bytes and historical API identity; retain standalone closeout path and checksum references."
        if path in functional_logs:
            item["job_id"] = functional_logs[path]["job_id"]
            item["raw_uncompressed_sha256"] = functional_logs[path]["raw_utf8_sha256"]
            item["provenance_references"].append(EVIDENCE + "final-integrated/functional/raw-log-manifest.json")
        if path in metadata:
            sidecar, source = metadata[path]
            for target, keys in {
                "source_sha": ("source_sha",), "source_git_tree": ("source_git_tree", "source_tree"),
                "run_id": ("run_id",), "job_id": ("job_id",), "command": ("command",),
                "expires_at": ("expires_at",), "execution_checkout_sha": ("execution_checkout_sha",),
                "uncommitted_fixture_or_fix": ("uncommitted_fixture_or_fix",),
            }.items():
                for key in keys:
                    if key in source:
                        item[target] = source[key]
                        break
            item["provenance_references"].append(sidecar)
            if source.get("classification", "").startswith("HISTORICAL"):
                item["classification"] = "HISTORICAL"
            item["classification_reason"] = "Path and SHA-256 exactly match original artifact sidecar; execution claims inherited without expansion."
        if digest in artifact_by_digest and path.endswith(".zip"):
            artifact = artifact_by_digest[digest]
            item.update(artifact_id=artifact["id"], hosted_artifact_expires_at=artifact["expires_at"])
            item["provenance_references"].append(EVIDENCE + "final-integrated/coordinator/artifact-index.json")
        item["provenance_references"] = sorted(set(item["provenance_references"]))
        entries.append(item)
    groups = []
    for group in inventory["exact_blob_duplicate_groups"]:
        hashes = {sha256((ROOT / path).read_bytes()) for path in group["paths"]}
        if len(hashes) != 1:
            raise ValueError("Duplicate group bytes changed: " + group["git_blob"])
        target = next((preferred[p] for p in group["paths"] if p in preferred), None)
        groups.append({
            "git_blob": group["git_blob"], "sha256": hashes.pop(), "paths": group["paths"],
            "canonical_path": target, "removal_allowed": False,
            "decision": "Retain original contextual paths and checksum references; navigation canonicalized." if target else "Retain: equal bytes can be separate execution outcomes, fixtures or independent review records.",
        })
    head = git("rev-parse", "HEAD").decode().strip()
    output = {
        "schema_version": 1,
        "generated_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "status": status,
        "qualification_scope_note": "Status is a coordinator-supplied catalog label; actual qualification is documented separately. Manifest generation executes no runtime gate.",
        "catalog_checkout_sha": head,
        "catalog_checkout_tree": git("rev-parse", "HEAD^{tree}").decode().strip(),
        "catalog_note": "Catalog SHA is documentation context, not a claim that these different executions used one source. Canonical files may be staged additions. No tests/scans are launched by this script.",
        "original_preservation_source": BASELINE,
        "original_preservation_tree": git("rev-parse", BASELINE + "^{tree}").decode().strip(),
        "original_preservation_count": len(baseline),
        "original_workstream_evidence_count": sum(p.startswith(EVIDENCE) for p in baseline),
        "original_other_repository_evidence_count": sum(not p.startswith(EVIDENCE) for p in baseline),
        "original_bytes_preserved": sum(row["bytes"] for row in baseline.values()),
        "original_files_modified_or_removed": 0,
        "entry_count": len(entries),
        "classifications": dict(sorted(collections.Counter(e["classification"] for e in entries).items())),
        "exact_duplicate_group_count": len(groups),
        "duplicate_groups": groups,
        "original_hosted_artifacts": artifact_index,
        "metadata_policy": "Null means unmapped/not applicable, never guessed. Exact sidecar path+digest matches can supply source/command fields. Existing multi-source manifests remain the authority for older executions. Hosted expiry does not expire a Git copy.",
        "entries": entries,
    }
    # One row per file keeps a complete catalog reviewable without thousands
    # of repeated formatting-only lines in the PR. This remains ordinary JSON.
    header = {key: value for key, value in output.items() if key != "entries"}
    encoded = json.dumps(header, indent=2)[:-2] + ',\n  "entries": [\n'
    encoded += ",\n".join("    " + json.dumps(row, separators=(",", ":")) for row in entries)
    (ROOT / OUTPUT).write_text(encoded + "\n  ]\n}\n")
    print(json.dumps({k: output[k] for k in ("entry_count", "classifications", "original_preservation_count", "original_bytes_preserved", "exact_duplicate_group_count", "original_files_modified_or_removed")}, sort_keys=True))


def verify():
    manifest = read_json(OUTPUT)
    baseline = baseline_hashes(tree_entries(BASELINE))
    recorded = {row["path"]: row for row in manifest["entries"]}
    for path, expected in baseline.items():
        if path not in recorded:
            raise ValueError("Original evidence missing from manifest: " + path)
        raw = (ROOT / path).read_bytes()
        if len(raw) != expected["bytes"] or sha256(raw) != expected["sha256"]:
            raise ValueError("Original evidence is not preserved: " + path)
    for path, expected in recorded.items():
        raw = (ROOT / path).read_bytes()
        if len(raw) != expected["bytes"] or sha256(raw) != expected["sha256"]:
            raise ValueError("Indexed bytes changed: " + path)
    for group in manifest["duplicate_groups"]:
        if any(sha256((ROOT / p).read_bytes()) != group["sha256"] for p in group["paths"]):
            raise ValueError("Duplicate group changed")
    print(json.dumps({"result": "PASS", "indexed_files": len(recorded), "original_files": len(baseline), "duplicate_groups": len(manifest["duplicate_groups"])}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify", action="store_true")
    parser.add_argument("--status", default="IN_PROGRESS", choices=(
        "IN_PROGRESS", "NOT_READY", "BLOCKED", "CONDITIONAL", "READY_FOR_MANUAL_MERGE",
    ))
    arguments = parser.parse_args()
    verify() if arguments.verify else build(arguments.status)
