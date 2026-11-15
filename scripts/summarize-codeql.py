#!/usr/bin/env python3
"""Preserve CodeQL findings and their SARIF rule metadata for human review.

CodeQL puts rule descriptors in tool.extensions, not necessarily tool.driver.
A successful analysis is evidence of execution, never a clean-security verdict.
"""

import argparse
from collections import Counter
import hashlib
import json
import os
from pathlib import Path


def resolve_rule(run, result):
    tool = run["tool"]
    driver = tool["driver"]
    extensions = tool.get("extensions", [])
    reference = result.get("rule", {})
    rule_id = result.get("ruleId", reference.get("id"))
    if reference.get("id", rule_id) != rule_id:
        raise ValueError("Conflicting SARIF rule identifiers")
    component_reference = reference.get("toolComponent", {})
    if "index" in component_reference:
        index = component_reference["index"]
        if type(index) is not int or not 0 <= index < len(extensions):
            raise ValueError("Invalid SARIF tool component index")
        component = extensions[index]
    elif "name" in component_reference:
        candidates = [c for c in [driver, *extensions]
                      if c.get("name") == component_reference["name"]]
        if len(candidates) != 1:
            raise ValueError("Unknown or ambiguous SARIF tool component")
        component = candidates[0]
    else:
        component = driver
    if ("name" in component_reference and
            component.get("name") != component_reference["name"]):
        raise ValueError("Conflicting SARIF tool component identifiers")
    rules = component.get("rules", [])
    index = reference.get("index", result.get("ruleIndex"))
    if index is not None:
        if type(index) is not int or not 0 <= index < len(rules):
            raise ValueError("Invalid SARIF rule index")
        rule = rules[index]
        if rule_id is not None and rule["id"] != rule_id:
            raise ValueError("SARIF rule index does not match identifier")
        return component, rule
    candidates = [rule for rule in rules if rule.get("id") == rule_id]
    if len(candidates) != 1:
        raise ValueError(f"Missing or ambiguous rule descriptor: {rule_id}")
    return component, candidates[0]


def summarize(paths):
    findings = []
    reports = []
    for path in paths:
        raw = path.read_bytes()
        data = json.loads(raw)
        runs = data.get("runs")
        if not isinstance(runs, list) or not runs:
            raise ValueError(f"No SARIF analysis runs in {path}")
        for run in runs:
            invocations = run.get("invocations", [])
            if not invocations or any(i.get("executionSuccessful") is not True
                                      for i in invocations):
                raise ValueError(f"SARIF analysis did not complete successfully: {path}")
            results = run.get("results")
            if not isinstance(results, list):
                raise ValueError(f"Missing SARIF results array in {path}")
            driver = run["tool"]["driver"]
            reports.append({
                "file": path.name,
                "sha256": hashlib.sha256(raw).hexdigest(),
                "tool": driver["name"],
                "version": driver.get("semanticVersion", driver.get("version")),
                "automationId": run.get("automationDetails", {}).get("id"),
                "resultCount": len(results),
                # Descriptors include diagnostic/metric rules. This is NOT
                # the number of queries evaluated; use the execution log.
                "ruleDescriptorCount": sum(len(c.get("rules", [])) for c in
                                           [driver, *run["tool"].get("extensions", [])]),
            })
            for result in results:
                component, rule = resolve_rule(run, result)
                findings.append({
                    "rule": rule["id"],
                    "ruleComponent": component["name"],
                    "level": result.get("level", rule.get("defaultConfiguration", {}).get("level", "warning")),
                    "securitySeverity": rule.get("properties", {}).get("security-severity"),
                    "message": result.get("message", {}).get("text"),
                    "locations": [entry.get("physicalLocation") for entry in result.get("locations", [])],
                    "partialFingerprints": result.get("partialFingerprints", {}),
                })
    summary = {
        "schemaVersion": 1,
        "sourceCommit": os.environ.get("GITHUB_SHA"),
        "workflowRun": os.environ.get("GITHUB_RUN_ID"),
        "reports": reports,
        "resultCount": len(findings),
        "byRule": dict(sorted(Counter(f["rule"] for f in findings).items())),
        "byLevel": dict(sorted(Counter(f["level"] for f in findings).items())),
        "classification": "unreviewed",
    }
    return findings, summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    paths = sorted(args.directory.glob("*.sarif"))
    if not paths:
        raise SystemExit("No analysis report was produced")
    findings, summary = summarize(paths)
    (args.directory / "findings.json").write_text(json.dumps(findings, indent=2) + "\n")
    (args.directory / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    message = (f"CodeQL findings requiring classification: {len(findings)}\n"
               f"Levels: {json.dumps(summary['byLevel'], sort_keys=True)}\n"
               "Analysis completed; findings require review. CI success is not a security verdict.\n")
    print(message, end="")
    for finding in findings[:100]:
        print(json.dumps(finding))
    if len(findings) > 100:
        print("Remaining findings are retained in the artifact.")
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as output:
            output.write("## CodeQL analysis evidence\n\n" + message.replace("\n", "\n\n"))


if __name__ == "__main__":
    main()
