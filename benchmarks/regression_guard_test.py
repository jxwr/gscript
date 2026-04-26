import unittest
import sys
import tempfile
import json
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import regression_guard as rg


class RegressionGuardParsingTest(unittest.TestCase):
    def test_parse_time(self):
        self.assertEqual(rg.parse_time("result: 42\nTime: 0.123s\n"), 0.123)
        self.assertIsNone(rg.parse_time("no time here"))

    def test_parse_legacy_baseline_seconds(self):
        self.assertEqual(rg.parse_seconds("Time: 1.500s"), 1.5)
        self.assertEqual(rg.parse_seconds("2.5ms"), 0.0025)
        self.assertIsNone(rg.parse_seconds("N/A"))
        self.assertIsNone(rg.parse_seconds("Time: s"))

    def test_loads_legacy_and_guard_baseline_schemas(self):
        with tempfile.TemporaryDirectory() as td:
            legacy = Path(td) / "legacy.json"
            legacy.write_text(json.dumps({"results": {"fib": {"jit": "Time: 1.500s"}}}))
            self.assertEqual(rg.load_baseline(legacy), {"fib": 1.5})

            guard = Path(td) / "guard.json"
            guard.write_text(
                json.dumps(
                    {
                        "results": [
                            {
                                "benchmark": "sieve",
                                "default": {"seconds": 0.088},
                            }
                        ]
                    }
                )
            )
            self.assertEqual(rg.load_baseline(guard), {"sieve": 0.088})

    def test_parse_jit_and_exit_stats(self):
        sample = rg.parse_sample(
            """Time: 0.010s
JIT Statistics:
  Tier 2 attempted: 3
  Tier 2 compiled: 2 functions
  Tier 2 entered:  1 functions
  Tier 2 failed: 1 functions
Tier 2 Exit Profile:
  total exits: 7
""",
            "ok",
            0,
        )
        self.assertEqual(sample.seconds, 0.01)
        self.assertEqual(sample.t2_attempted, 3)
        self.assertEqual(sample.t2_entered, 1)
        self.assertEqual(sample.t2_failed, 1)
        self.assertEqual(sample.exit_total, 7)

    def test_summarize_keeps_partial_success(self):
        samples = [
            rg.RunSample(status="timeout"),
            rg.RunSample(status="ok", seconds=0.3, t2_attempted=2, t2_entered=1),
            rg.RunSample(status="ok", seconds=0.1, t2_attempted=4, t2_entered=3),
        ]
        result = rg.summarize_samples(samples)
        self.assertEqual(result.status, "partial")
        self.assertEqual(result.seconds, 0.2)
        self.assertEqual(result.t2_attempted, 4)
        self.assertEqual(result.t2_entered, 3)

    def test_report_row_calculates_ratios(self):
        row = rg.BenchmarkResult(
            benchmark="sieve",
            vm=rg.ModeResult(status="ok", seconds=0.2),
            default=rg.ModeResult(status="ok", seconds=0.05, t2_attempted=1, t2_entered=1),
            no_filter=rg.ModeResult(status="ok", seconds=0.04),
            luajit=rg.ModeResult(status="ok", seconds=0.01),
            baseline_seconds=0.045,
            regression_pct=11.111,
            regression=True,
        )
        out = rg.report_row(row)
        self.assertEqual(out["jit_vm_speedup"], 4.0)
        self.assertEqual(out["jit_luajit_ratio"], 5.0)
        self.assertEqual(out["t2_attempted"], 1)
        self.assertTrue(out["regression"])

    def test_writes_csv_and_markdown_summary(self):
        row = rg.BenchmarkResult(
            benchmark="fib",
            vm=rg.ModeResult(status="ok", seconds=1.0),
            default=rg.ModeResult(status="ok", seconds=0.5, t2_attempted=2, t2_entered=1),
            luajit=rg.ModeResult(status="skipped"),
            baseline_seconds=0.4,
            regression_pct=25.0,
            regression=True,
        )
        with tempfile.TemporaryDirectory() as td:
            csv_path = Path(td) / "guard.csv"
            md_path = Path(td) / "guard.md"
            rg.write_csv(csv_path, [row])
            rg.write_markdown(md_path, [row], 10.0)

            self.assertIn("benchmark,vm_seconds", csv_path.read_text())
            self.assertIn("fib,1.0,0.5", csv_path.read_text())
            markdown = md_path.read_text()
            self.assertIn("| fib | 1.000s | 0.500s", markdown)
            self.assertIn("REG +25.0%", markdown)


if __name__ == "__main__":
    unittest.main()
