import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import audit_guard as audit


class AuditGuardTest(unittest.TestCase):
    def test_load_rows_and_report_sections(self):
        payload = {
            "results": [
                {
                    "benchmark": "fast",
                    "default": {"seconds": 0.002, "exit_total": 0},
                    "luajit": {"status": "ok", "seconds": 0.004},
                },
                {
                    "benchmark": "missing_ref",
                    "default": {"seconds": 0.020, "exit_total": 25},
                    "luajit": {"status": "missing", "seconds": None},
                },
                {
                    "benchmark": "tiny",
                    "default": {"seconds": 0.0, "exit_total": 0},
                    "luajit": {"status": "ok", "seconds": 0.010},
                },
            ]
        }
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "guard.json"
            path.write_text(json.dumps(payload))
            rows = audit.load_rows(path)

        self.assertEqual([r.name for r in rows], ["fast", "missing_ref", "tiny"])
        report = audit.markdown_report(rows, low_resolution_cutoff=0.001, exit_cutoff=20)
        self.assertIn("## Confirmed LuaJIT Comparisons", report)
        self.assertIn("| fast | 0.002s | 0.004s | 0.50x |", report)
        self.assertIn("| missing_ref | missing | 0.020s |", report)
        self.assertIn("| tiny | 0.000s | Needs calibrated repeats or ns/op bench |", report)
        self.assertIn("| missing_ref | 25 |", report)

    def test_rejects_legacy_results_map(self):
        with tempfile.TemporaryDirectory() as td:
            path = Path(td) / "legacy.json"
            path.write_text(json.dumps({"results": {"fib": {}}}))
            with self.assertRaises(ValueError):
                audit.load_rows(path)


if __name__ == "__main__":
    unittest.main()
