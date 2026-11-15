import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("summarize_codeql", Path(__file__).parents[1] / "summarize-codeql.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class SummarizeCodeQLTest(unittest.TestCase):
    def setUp(self):
        self.rule = {"id": "go/example", "defaultConfiguration": {"level": "error"},
                     "properties": {"security-severity": "8.1"}}
        self.run = {"tool": {"driver": {"name": "CodeQL", "rules": []},
                             "extensions": [{"name": "codeql/go-queries", "rules": [self.rule]}]},
                    "invocations": [{"executionSuccessful": True}],
                    "results": [{"ruleId": "go/example", "rule": {"id": "go/example", "index": 0,
                                 "toolComponent": {"index": 0}}, "message": {"text": "Review this"}}]}

    def summarize(self, runs):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "go.sarif"
            path.write_text(json.dumps({"runs": runs}))
            return module.summarize([path])

    def test_codeql_extension_metadata_is_not_lost(self):
        findings, summary = self.summarize([self.run])
        self.assertEqual(len(findings), 1)
        self.assertEqual(findings[0]["securitySeverity"], "8.1")
        self.assertEqual(findings[0]["level"], "error")
        self.assertEqual(summary["resultCount"], 1)
        self.assertEqual(summary["classification"], "unreviewed")
        self.assertEqual(summary["byLevel"], {"error": 1})

    def test_driver_rules_and_result_level_override(self):
        self.run["tool"]["driver"]["rules"] = [self.rule]
        self.run["results"] = [{"ruleId": "go/example", "ruleIndex": 0, "level": "note"}]
        findings, _ = self.summarize([self.run])
        self.assertEqual(findings[0]["level"], "note")
        self.assertEqual(findings[0]["securitySeverity"], "8.1")

    def test_component_name_resolution(self):
        self.run["results"][0]["rule"]["toolComponent"] = {"name": "codeql/go-queries"}
        findings, _ = self.summarize([self.run])
        self.assertEqual(findings[0]["ruleComponent"], "codeql/go-queries")

    def test_duplicate_ids_in_other_component_do_not_override(self):
        other = copy.deepcopy(self.rule)
        other["properties"]["security-severity"] = "1.0"
        self.run["tool"]["driver"]["rules"] = [other]
        findings, _ = self.summarize([self.run])
        self.assertEqual(findings[0]["securitySeverity"], "8.1")

    def test_invalid_references_fail_instead_of_silent_null_metadata(self):
        for replacement in [{"id": "wrong", "index": 0, "toolComponent": {"index": 0}},
                            {"id": "go/example", "index": 5, "toolComponent": {"index": 0}},
                            {"id": "go/example", "index": 0, "toolComponent": {"index": -1}},
                            {"id": "go/example", "index": 0, "toolComponent": {"index": 0, "name": "wrong"}}]:
            with self.subTest(reference=replacement):
                run = copy.deepcopy(self.run)
                run["results"][0]["rule"] = replacement
                with self.assertRaises(ValueError):
                    self.summarize([run])

    def test_incomplete_analysis_is_not_a_clean_report(self):
        for invocations in [[], [{"executionSuccessful": False}], [{}]]:
            with self.subTest(invocations=invocations):
                self.run["invocations"] = invocations
                with self.assertRaises(ValueError):
                    self.summarize([self.run])
        with self.assertRaises(ValueError):
            self.summarize([])

    def test_conflicting_rule_indices_are_rejected(self):
        self.run["results"][0]["ruleIndex"] = 1
        with self.assertRaisesRegex(ValueError, "Conflicting SARIF rule indices"):
            self.summarize([self.run])

    def test_empty_completed_report_is_counted_without_a_security_verdict(self):
        self.run["results"] = []
        findings, summary = self.summarize([self.run])
        self.assertEqual(findings, [])
        self.assertEqual(summary["resultCount"], 0)
        self.assertEqual(summary["classification"], "unreviewed")

    def test_reports_and_results_are_not_deduplicated(self):
        findings, summary = self.summarize([self.run, self.run])
        self.assertEqual(len(findings), 2)
        self.assertEqual(summary["resultCount"], 2)
        self.assertEqual(len(summary["reports"]), 2)


if __name__ == "__main__":
    unittest.main()
