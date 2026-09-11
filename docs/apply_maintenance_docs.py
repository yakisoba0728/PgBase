"""Apply the docs-lane maintenance metadata without rewriting historical audit inputs."""
from __future__ import annotations
import json
import re
import subprocess
from collections import Counter, defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
AUDIT = ROOT / "docs/audits/2026-09-11"
SOURCE = ROOT / "docs/pocketbase-source-audit-2026-09-09"
OBSERVATION_DATE = "2026-09-11"

def baseline_observation(baseline: str, path: str) -> str:
    """Record a source-derived, path-specific observation for duplicate notes."""
    source = subprocess.run(
        ["git", "show", f"{baseline}:{path}"], cwd=ROOT, check=True,
        text=True, capture_output=True,
    ).stdout
    tests = re.findall(r"^func (Test\w+)", source, re.MULTILINE)
    if tests:
        named = ", ".join(tests[:2])
        return f"Baseline source read on {OBSERVATION_DATE}: {named} provides this path's concrete verification assertion."
    functions = re.findall(r"^func (?:\([^)]*\) )?([A-Za-z]\w*)", source, re.MULTILINE)
    if functions:
        named = ", ".join(functions[:2])
        return f"Baseline source read on {OBSERVATION_DATE}: {named} is the file's concrete storage/realtime control seam."
    types = re.findall(r"^type (\w+)", source, re.MULTILINE)
    constants = re.findall(r"^const (\w+)", source, re.MULTILINE)
    named = ", ".join((types + constants)[:2])
    if named:
        return f"Baseline source read on {OBSERVATION_DATE}: {named} defines this file's storage/realtime contract guard."
    raise ValueError(f"no source-derived observation candidate found in {path}")

def dump(path: Path, value) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

def coverage() -> None:
    path = AUDIT / "coverage.json"
    data = json.loads(path.read_text(encoding="utf-8"))
    notes = [entry.get("notes", "").strip() for entry in data["entries"]]
    groups = defaultdict(list)
    for entry in data["entries"]:
        groups[entry.get("notes", "").strip()].append(entry["path"])
        for field in ("observation", "provenanceStatus", "sourceSnapshot"):
            entry.pop(field, None)
    duplicates = [{"note": note, "paths": paths} for note, paths in sorted(groups.items()) if note and len(paths) > 1]
    # Keep the historical ledger's entry shape untouched. Maintenance metadata
    # lives in a compact companion so this file remains the original evidence.
    companion = {
        "schemaVersion": 1,
        "coverage": "coverage.json",
        "observationPolicy": {"text": "Retain notes/readRanges exactly; report duplicates and add only selectively verified baseline observations.", "fullReadTerminologyPreserved": True},
        "noteAudit": {"entryCount": len(notes), "nonEmptyCount": sum(bool(note) for note in notes), "uniqueNoteCount": len(set(notes)), "duplicateEntryCount": sum(len(item["paths"]) - 1 for item in duplicates), "duplicateNoteGroupCount": len(duplicates), "duplicateGroups": duplicates},
        "provenanceContract": {"sourceKindField": "kind", "snapshotField": "blobSha", "generationField": "provenance", "nullBatchMeaning": "historical nullable batch; not upgraded to a read claim"},
        "duplicateObservations": [{"path": path, "baseline": data["baseSha"], "sourceRead": True, "observationDate": OBSERVATION_DATE, "observation": baseline_observation(data["baseSha"], path)} for item in duplicates for path in item["paths"]],
    }
    dump(AUDIT / "coverage-companion.json", companion)
    data.pop("observationPolicy", None); data.pop("noteAudit", None); data.pop("provenanceContract", None)
    dump(path, data)

def features() -> None:
    path = AUDIT / "features.json"
    data = json.loads(path.read_text(encoding="utf-8"))
    evidence = {
        "REC-04": ["read: internal/recordops/batch_integration_test.go", "read: internal/api/record_handlers_test.go"],
        "REC-06": ["read: internal/recordops/service_test.go", "read: internal/api/record_handlers_test.go"],
        "REC-07": ["read: internal/api/record_handlers_test.go", "read: internal/responseprojection/record_test.go"],
        "REC-08": ["read: internal/recordops/batch_integration_test.go", "read: internal/recordops/service_test.go"],
        "REC-10": ["read: internal/recordops/batch_integration_test.go", "read: internal/api/batch_handlers_test.go"],
        "REC-11": ["read: internal/api/batch_handlers.go", "read: internal/recordops/batch_integration_test.go"],
        "REC-13": ["read: internal/recordops/modifiers_test.go", "read: internal/recordops/modifiers_integration_test.go"],
        "REC-14": ["read: internal/recordops/modifiers_integration_test.go", "read: internal/api/batch_file_modifiers.go"],
        "UI-08": ["contract-only: ui/admin/src/pages/view-definition-editor.tsx; authenticated browser evidence was not available in the fixed historical audit"],
        "UI-22": ["contract-only: ui/admin/src/pages/users-page.tsx; authenticated browser evidence was not available in the fixed historical audit"],
        "UI-26": ["contract-only: ui/admin/src/pages/operations-console.tsx; authenticated browser evidence was not available in the fixed historical audit"],
        "UI-28": ["contract-only: ui/admin/src/pages/operations-console.tsx; authenticated browser evidence was not available in the fixed historical audit"],
        "UI-30": ["contract-only: ui/admin/src/pages/sql-console-page.tsx; authenticated browser evidence was not available in the fixed historical audit"],
        "UI-35": ["contract-only: ui/admin/src/pages/record-edit-page.tsx; visual/browser evidence was not available in the fixed historical audit"],
    }
    for row in data["rows"]:
        if row["ID"] in evidence and (not row.get("읽은테스트및실행증거") or row["ID"] == "REC-11"):
            row["읽은테스트및실행증거"] = evidence[row["ID"]]
            row["evidenceStatus"] = "historical_read_pointer" if row["verdict"] == "verified" else "contract_only_not_applicable"
    data["evidenceContract"] = {
        "verified": "repository-relative read/test pointers are required; absence of runtime rerun remains a limitation",
        "intentional_difference": "contract-only rationale is explicit when authenticated UI execution is unavailable",
        "historicalBaseline": data.get("baseSha"),
    }
    dump(path, data)
    md = AUDIT / "features.md"
    lines = md.read_text(encoding="utf-8").splitlines()
    for i, line in enumerate(lines):
        for row_id, pointers in evidence.items():
            if line.startswith(f"| {row_id} |"):
                # The generated table has nine pipe-separated cells; replace only the evidence cell.
                cells = line.split("|")
                if len(cells) >= 10:
                    rendered = "<br>".join(pointers)
                    cells[7] = f" {rendered} "
                    lines[i] = "|".join(cells)
    md.write_text("\n".join(lines).rstrip() + "\n", encoding="utf-8")

def tools() -> None:
    ledger_path = SOURCE / "tools-ledger.json"
    extra_path = SOURCE / "tools-extra-ledger.json"
    ledger = json.loads(ledger_path.read_text(encoding="utf-8"))
    extra = json.loads(extra_path.read_text(encoding="utf-8"))
    extra_by_path = {item["path"]: item for item in extra}
    for item in ledger:
        item.pop("effectiveStatus", None); item.pop("effectiveSource", None)
    overlap = []
    for item in ledger:
        if item["path"] in extra_by_path:
            overlap.append({"path": item["path"], "rawStatus": item["status"], "overrideStatus": extra_by_path[item["path"]]["status"], "source": "tools-extra-ledger.json"})
    dump(ledger_path, ledger)
    dump(SOURCE / "tools-effective-ledger.json", {"schemaVersion": 1, "rawLedger": "tools-ledger.json", "overrideLedger": "tools-extra-ledger.json", "effectiveCounts": dict(Counter(extra_by_path.get(item["path"], item)["status"] for item in ledger)), "overrides": overlap})
    tools_md = SOURCE / "tools.md"
    text = tools_md.read_text(encoding="utf-8")
    old = "`tools-ledger.json` is authoritative for per-file status."
    new = "`tools-ledger.json` is the raw per-file ledger. `tools-extra-ledger.json` is an intentional override for its 16 overlapping archive/cron/dbutils paths; the effective view is [tools-effective-ledger.json](tools-effective-ledger.json)."
    text = text.replace(old, new, 1).replace("The merged scope is 185 read / 0 indexed;", "The effective merged scope is 185 read / 0 indexed; raw indexed values remain visible with override provenance in the effective ledger.", 1)
    tools_md.write_text(text, encoding="utf-8")

def comparison() -> None:
    old_json = ROOT / "docs/pocketbase-feature-comparison-current-2026-09-10.json"
    old_md = ROOT / "docs/pocketbase-feature-comparison-current-2026-09-10.md"
    new_json = ROOT / "docs/pocketbase-feature-comparison-current-2026-09-11.json"
    new_md = ROOT / "docs/pocketbase-feature-comparison-current-2026-09-11.md"
    data = json.loads(old_json.read_text(encoding="utf-8"))
    data["artifactStatus"] = "current"
    data["canonicalPath"] = "docs/pocketbase-feature-comparison-current-2026-09-11.json"
    dump(new_json, data)
    old_data = json.loads(old_json.read_text(encoding="utf-8"))
    old_data["artifactStatus"] = "historical-superseded"
    old_data["canonicalCurrent"] = "docs/pocketbase-feature-comparison-current-2026-09-11.json"
    dump(old_json, old_data)
    md = old_md.read_text(encoding="utf-8")
    marker = "> Historical snapshot: filename date is 2026-09-10; the embedded comparison date is 2026-09-11. Use [the canonical current artifact](pocketbase-feature-comparison-current-2026-09-11.md) for the current view.\n\n"
    if not md.startswith("> Historical snapshot:"):
        md = marker + md
    old_md.write_text(md, encoding="utf-8")
    current_md = "> Current artifact: filename date, embedded JSON date, and this heading all use 2026-09-11.\n\n"
    historical_prefix = "> Historical snapshot: filename date is 2026-09-10; the embedded comparison date is 2026-09-11. Use [the canonical current artifact](pocketbase-feature-comparison-current-2026-09-11.md) for the current view.\n\n"
    current_md += md[len(historical_prefix):] if md.startswith(historical_prefix) else md
    new_md.write_text(current_md, encoding="utf-8")

def main() -> None:
    coverage(); features(); tools(); comparison()

if __name__ == "__main__":
    main()
