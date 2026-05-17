import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import perf_submit_guard as guard


def timing_payload(rows):
    return {
        "results": [
            {
                "benchmark": name.split("/", 1)[1],
                "group": name.split("/", 1)[0],
                "modes": {
                    "default": {
                        "current": {"status": "ok", "stats": {"median": current}},
                        "luajit": {"status": "ok", "stats": {"median": luajit}},
                    }
                },
            }
            for name, current, luajit in rows
        ]
    }


class PerfSubmitGuardTest(unittest.TestCase):
    def test_rejects_luajit_ratio_above_threshold(self):
        rows = guard.load_rows(write_json(timing_payload([("suite/a", 0.81, 1.0)])))
        violations = guard.check_rows(rows, ratio_threshold=0.8)
        self.assertEqual([(v.kind, v.name) for v in violations], [("luajit", "suite/a")])

    def test_rejects_regression_against_baseline(self):
        candidate = guard.load_rows(write_json(timing_payload([("suite/a", 0.75, 1.0)])))
        baseline = guard.load_rows(write_json(timing_payload([("suite/a", 0.70, 1.0)])))
        violations = guard.check_rows(candidate, baseline=baseline, ratio_threshold=0.8, regression_tolerance=0.03)
        self.assertEqual([(v.kind, v.name) for v in violations], [("regression", "suite/a")])

    def test_accepts_under_threshold_without_regression(self):
        candidate = guard.load_rows(write_json(timing_payload([("suite/a", 0.72, 1.0)])))
        baseline = guard.load_rows(write_json(timing_payload([("suite/a", 0.71, 1.0)])))
        self.assertEqual(guard.check_rows(candidate, baseline=baseline, ratio_threshold=0.8), [])


def write_json(payload):
    td = tempfile.TemporaryDirectory()
    path = Path(td.name) / "timing.json"
    path.write_text(json.dumps(payload))
    write_json.keepalive.append(td)
    return path


write_json.keepalive = []


if __name__ == "__main__":
    unittest.main()
