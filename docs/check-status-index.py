"""Validate status applicability metadata and locally resolvable Git commits."""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument("--index", type=Path, help="index path; defaults to this checkout's index")
parser.add_argument("--repo", type=Path, help="Git repository to resolve commits in; defaults to this checkout")
args = parser.parse_args()
root = Path(__file__).resolve().parents[1]
index_path = (args.index or (root / "docs/pocketbase-status-index.json")).resolve()
repo = (args.repo or root).resolve()
index = json.loads(index_path.read_text(encoding="utf-8"))
sha = re.compile(r"^[0-9a-f]{7,40}$")
allowed_applicability = {"historical_snapshot", "historical_handoff", "current_status_with_historical_sections"}
errors = []
documents = index.get("documents")
if not isinstance(documents, list) or not documents:
    errors.append("documents must be a non-empty list")
    documents = []
for item in documents:
    if not isinstance(item, dict):
        errors.append("document entry must be an object")
        continue
    path_value = item.get("path", "")
    path = root / path_value
    if not path_value or not path.is_file(): errors.append(f"missing document: {path_value}")
    applicability = item.get("applicability")
    if applicability not in allowed_applicability:
        errors.append(f"invalid or missing applicability for {path_value}: {applicability!r}")
    for key in ("baselineSha", "verificationSha"):
        value = item.get(key, "")
        if not sha.fullmatch(value):
            errors.append(f"invalid {key} format for {path_value}: {value!r}")
            continue
        resolved = subprocess.run(
            ["git", "rev-parse", "--verify", f"{value}^{{commit}}"],
            cwd=repo,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
        )
        if resolved.returncode != 0:
            errors.append(f"unresolvable {key} for {path_value}: {value}")
if errors:
    print("\n".join(errors)); sys.exit(1)
print(f"status index valid: {len(documents)} documents; applicability and {len(documents) * 2} local commit references verified")
