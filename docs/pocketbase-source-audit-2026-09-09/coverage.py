"""Check audit bookkeeping and pinned bytes; this does not prove human/model reading."""
import hashlib
import json
from collections import Counter, defaultdict
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent.parent
UPSTREAM = ROOT / ".local/upstream/pocketbase"
inventory = json.loads((HERE / "inventory.json").read_text())
files = {entry["path"]: entry for entry in inventory["files"]}
assert len(files) == len(inventory["files"]), "Duplicate inventory paths"

merged = {}
for owner in ("api", "core", "ui", "tools", "operations"):
    entries = json.loads((HERE / f"{owner}-ledger.json").read_text())
    paths = [entry["path"] for entry in entries]
    expected = {path for path, entry in files.items() if entry["owner"] == owner}
    assert len(paths) == len(set(paths)), f"Duplicate {owner} ledger paths"
    assert set(paths) == expected, f"Wrong {owner} ledger scope"
    merged.update({entry["path"]: {**entry, "ledger": f"{owner}-ledger.json"} for entry in entries})

overrides = {
    "core-fields": lambda path: path.startswith("core/field") and path.endswith(".go"),
    "ui-css": lambda path: path.startswith("ui/src/css/"),
    "tools-extra": lambda path: path.startswith(("tools/archive/", "tools/cron/", "tools/dbutils/")),
}
for name, matches in overrides.items():
    entries = json.loads((HERE / f"{name}-ledger.json").read_text())
    paths = [entry["path"] for entry in entries]
    assert len(paths) == len(set(paths)), f"Duplicate {name} override paths"
    assert set(paths) == {path for path in files if matches(path)}, f"Wrong {name} override scope"
    merged.update({entry["path"]: {**entry, "ledger": f"{name}-ledger.json"} for entry in entries})

allowed = {"read", "indexed", "inspected_artifact", "inspected_reference"}
by_owner = defaultdict(Counter)
for path, entry in merged.items():
    assert entry["status"] in allowed, f"Unknown status: {path}"
    assert entry["note"].strip(), f"Missing note: {path}"
    actual = hashlib.sha256((UPSTREAM / path).read_bytes()).hexdigest()
    assert actual == files[path]["sha256"], f"Upstream bytes changed: {path}"
    by_owner[files[path]["owner"]][entry["status"]] += 1

summary = {
    "upstream": inventory["commit"],
    "pgbase": inventory["pgbaseCommit"],
    "total": len(merged),
    "statuses": dict(Counter(entry["status"] for entry in merged.values())),
    "byOwner": {owner: dict(counts) for owner, counts in sorted(by_owner.items())},
    "pending": sorted(path for path, entry in merged.items() if entry["status"] == "indexed"),
    "files": [merged[path] for path in sorted(merged)],
}
(HERE / "coverage.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({key: value for key, value in summary.items() if key not in ("pending", "files")}, indent=2))
print(f"Pending source files: {len(summary['pending'])}")
