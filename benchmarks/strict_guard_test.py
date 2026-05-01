import argparse
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import strict_guard as sg


class StrictGuardParsingTest(unittest.TestCase):
    def test_parse_time_and_counters(self):
        run = sg.parse_command_run(
            """result: 42
Time: 0.123s
JIT Statistics:
  Tier 2 attempted: 3
  Tier 2 entered:  2 functions
  Tier 2 failed: 1 functions
Tier 2 Exit Profile:
  total exits: 9
""",
            "ok",
            0,
        )
        self.assertEqual(run.status, "ok")
        self.assertEqual(run.seconds, 0.123)
        self.assertEqual(run.t2_attempted, 3)
        self.assertEqual(run.t2_entered, 2)
        self.assertEqual(run.t2_failed, 1)
        self.assertEqual(run.exit_total, 9)

    def test_no_time_is_explicit(self):
        run = sg.parse_command_run("result only\n", "ok", 0)
        self.assertEqual(run.status, "no_time")
        self.assertIsNone(run.seconds)


class StrictGuardStatisticsTest(unittest.TestCase):
    def test_compute_stats_reports_spread(self):
        stats = sg.compute_stats([1.0, 2.0, 4.0])
        self.assertEqual(stats.n, 3)
        self.assertEqual(stats.median, 2.0)
        self.assertEqual(stats.min, 1.0)
        self.assertEqual(stats.max, 4.0)
        self.assertAlmostEqual(stats.stdev, 1.527525, places=6)
        self.assertEqual(stats.mad, 1.0)
        self.assertAlmostEqual(stats.cv_pct, 65.465367, places=6)

    def test_low_resolution_sample_is_not_comparable(self):
        runs = [
            sg.CommandRun(status="ok", seconds=0.0, wall_seconds=0.002),
            sg.CommandRun(status="ok", seconds=0.0, wall_seconds=0.002),
        ]
        sample = sg.summarize_repeated_runs(
            runs,
            repeat=2,
            timer_resolution=0.001,
            min_sample_seconds=0.020,
            allow_wall_time=False,
        )
        self.assertEqual(sample.status, "low_resolution")
        mode = sg.summarize_mode([sample], [], repeat=2)
        self.assertEqual(mode.status, "low_resolution")
        self.assertIsNone(sg.comparable_seconds(mode))

    def test_wall_time_fallback_records_source(self):
        runs = [
            sg.CommandRun(status="ok", seconds=0.0, wall_seconds=0.020),
            sg.CommandRun(status="ok", seconds=0.0, wall_seconds=0.022),
        ]
        sample = sg.summarize_repeated_runs(
            runs,
            repeat=2,
            timer_resolution=0.001,
            min_sample_seconds=0.020,
            allow_wall_time=True,
        )
        self.assertEqual(sample.status, "ok")
        self.assertEqual(sample.time_source, "wall_repeat")
        self.assertAlmostEqual(sample.seconds, 0.021)

    def test_script_repeats_average_total_time(self):
        runs = [
            sg.CommandRun(status="ok", seconds=0.015, wall_seconds=0.020),
            sg.CommandRun(status="ok", seconds=0.017, wall_seconds=0.021),
        ]
        sample = sg.summarize_repeated_runs(
            runs,
            repeat=2,
            timer_resolution=0.001,
            min_sample_seconds=0.020,
            allow_wall_time=False,
        )
        self.assertEqual(sample.status, "ok")
        self.assertEqual(sample.time_source, "script")
        self.assertAlmostEqual(sample.seconds, 0.016)


class StrictGuardReportTest(unittest.TestCase):
    def test_markdown_marks_unreliable_results_without_ratio(self):
        row = sg.BenchmarkResult("tiny")
        row.modes["vm"] = sg.ModeResult("low_resolution")
        row.modes["default"] = sg.ModeResult(
            "ok",
            stats=sg.Stats(n=3, median=0.010, min=0.009, max=0.011, stdev=0.001, mad=0.001, cv_pct=10.0),
        )
        args = argparse.Namespace(
            warmup=1,
            runs=3,
            min_sample_seconds=0.02,
            timer_resolution=0.001,
            allow_wall_time=False,
        )
        markdown = sg.markdown_summary([row], ["vm", "default"], args)
        self.assertIn("| tiny | - | - | vm:low_resolution |", markdown)
        self.assertIn("| tiny | vm | low_resolution |", markdown)

    def test_repeat_overrides_accept_bench_and_mode_bench(self):
        overrides = sg.parse_repeat_overrides(["fib=4", "default/sieve=8"])
        self.assertEqual(sg.repeat_for(overrides, "vm", "fib"), 4)
        self.assertEqual(sg.repeat_for(overrides, "default", "sieve"), 8)
        self.assertIsNone(sg.repeat_for(overrides, "vm", "sieve"))


if __name__ == "__main__":
    unittest.main()
