#!/usr/bin/env python3
"""Compare reviewed request paths; does not rerun or qualify any load gate."""
import hashlib
import json
import subprocess
from pathlib import Path

ROOT = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
OUT = Path(__file__).resolve().parent
PASS = "4091c3be20ec8db177886a2a85550924fce37ff5"
FAIL = "89e883fc700ef0250024427a1f09bb88affe0383"
START = "77bc070047c5a19d68439b75cb7b65bcde5d1c9a"
SCOPES = [
    "internal/database", "internal/query", "internal/recordops", "internal/schema",
    "internal/userauth", "internal/membership", "internal/api/user_handlers.go",
    "internal/api/record_handlers.go", "pgbase/open_loop_load_integration_test.go",
    "pgbase/open_loop_diagnostics_integration_test.go", "pgbase/mixed_load_integration_test.go",
    ".github/workflows/full-qualification.yml", "scripts/support/run-observed-load.py",
]

def git(*args):
    return subprocess.check_output(["git", "-C", str(ROOT), *args])

def entries(revision, scope):
    return git("ls-tree", "-rz", revision, "--", scope)

result = {
    "historical_passing_source": PASS, "failed_qualification_source": FAIL,
    "recovery_start_source": START,
    "source_trees": {r: git("rev-parse", r + "^{tree}").decode().strip() for r in [PASS, FAIL, START]},
    "scope": "Entire tree entries under explicitly reviewed request/runtime and load-harness paths; no whole-project audit claim.",
    "scopes": [],
    "conclusion": "NO_CHANGE_IN_REVIEWED_RUNTIME_OR_PROTOCOL_PATHS; this does not prove environmental sole causation or absence of a pre-existing defect.",
}
for scope in SCOPES:
    raw = {r: entries(r, scope) for r in [PASS, FAIL, START]}
    changed = git("diff", "--name-only", PASS, START, "--", scope).decode().splitlines()
    result["scopes"].append({
        "path": scope,
        "entry_counts": {r: len([e for e in b.split(b"\0") if e]) for r, b in raw.items()},
        "mode_blob_path_listing_sha256": {r: hashlib.sha256(b).hexdigest() for r, b in raw.items()},
        "identical": len(set(raw.values())) == 1,
        "changed_paths_pass_to_recovery": changed,
    })
result["all_selected_entries_identical"] = all(s["identical"] for s in result["scopes"])
(OUT / "runtime-scope-comparison.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({"all_selected_entries_identical": result["all_selected_entries_identical"], "scope_count": len(SCOPES)}))
