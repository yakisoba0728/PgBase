#!/usr/bin/env python3
"""Validate this results package without depending on historical status tooling."""

import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
here = Path(__file__).resolve().parent
data = json.loads((here / "results.json").read_text())
readme = (here / "README.md").read_text()
items = data["items"]
ids = [item["id"] for item in items]
errors = []

if data.get("productCommit") != "0228716":
    errors.append("productCommit must be 0228716")
if len(items) != 45 or len(set(ids)) != 45:
    errors.append("results.json must contain 45 unique approved IDs")
table_ids = [value for value in re.findall(r"^\| ([A-Z0-9][A-Z0-9-]+) \|", readme, re.M) if value != "ID"]
if table_ids != ids:
    errors.append("README table IDs must match results.json in order")
for item in items:
    for relative in item.get("files", []):
        if not (root / relative).exists():
            errors.append(f"{item['id']}: missing source/test pointer {relative}")
for target in re.findall(r"\]\((\.\./\.\./\.\./[^)]+)\)", readme):
    if not (here / target).resolve().exists():
        errors.append(f"README: missing link target {target}")
if data.get("finalVerification") != "passed_with_documented_optional_skips":
    errors.append("final verification status is incorrect")
verification = here / "verification.json"
if not verification.exists():
    errors.append("verification.json is required")
else:
    report = json.loads(verification.read_text())
    if report.get("status") != "passed_with_documented_optional_skips":
        errors.append("verification status is incorrect")
    if not report.get("productCommit", "").startswith(data.get("productCommit", "")):
        errors.append("verification product commit does not match results")
    if not any(check.get("command") == "npm run verify:dist" and check.get("result") == "pass" for check in report.get("checks", [])):
        errors.append("verification lacks final UI reproducibility evidence")
metrics = here / "metrics.json"
if not metrics.exists():
    errors.append("metrics.json is required")
else:
    metric_data = json.loads(metrics.read_text())
    if metric_data.get("source", {}).get("kind") != "product-stage-final-source":
        errors.append("metrics source kind is incorrect")

if errors:
    print("FAIL")
    print("\n".join(errors))
    sys.exit(1)
print(f"PASS: {len(items)} unique IDs; {sum(len(x.get('files', [])) for x in items)} source/test pointers")
