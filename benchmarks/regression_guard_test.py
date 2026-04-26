import unittest
import sys
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


if __name__ == "__main__":
    unittest.main()
