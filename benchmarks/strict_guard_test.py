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
        self.assertEqual(run.output_hash, sg.output_hash("result only\n"))

    def test_output_hash_ignores_timing_and_jit_stats(self):
        a = sg.output_hash(
            """checksum: 123
Time: 0.010s
JIT Statistics:
  Tier 2 attempted: 1
  Tier 2 entered:  1 functions
Tier 2 Exit Profile:
  total exits: 0
"""
        )
        b = sg.output_hash("checksum: 123\nTime: 0.020s\n")
        self.assertEqual(a, b)
        self.assertEqual(sg.checksum_text("checksum: 123\nTime: 0.020s\n"), "123")

    def test_output_hash_ignores_embedded_subbenchmark_times(self):
        a = sg.output_hash(
            """int_array_sum:    0.004s (result=5000050000)
array_swap:       0.006s (result=100000)
Time: 0.020s
"""
        )
        b = sg.output_hash(
            """int_array_sum:    0.043s (result=5000050000)
array_swap:       0.307s (result=100000)
Time: 0.462s
"""
        )
        self.assertEqual(a, b)


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

    def test_checksum_mismatch_is_explicit(self):
        samples = [
            sg.Sample(status="ok", seconds=1.0, runs=[sg.CommandRun(status="ok", seconds=1.0, output_hash="aaa")]),
            sg.Sample(status="ok", seconds=1.0, runs=[sg.CommandRun(status="ok", seconds=1.0, output_hash="bbb")]),
        ]
        mode = sg.summarize_mode(samples, [], repeat=1)
        self.assertEqual(mode.checksum_status, "mismatch")
        self.assertEqual(mode.output_hash, "aaa,bbb")


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
        self.assertIn("| suite/tiny | - | - | vm:low_resolution |", markdown)
        self.assertIn("| suite/tiny | vm | low_resolution |", markdown)

    def test_repeat_overrides_accept_bench_group_bench_and_mode_bench(self):
        overrides = sg.parse_repeat_overrides(["fib=4", "suite/fib=6", "default/sieve=8"])
        self.assertEqual(sg.repeat_for(overrides, "vm", "fib"), 4)
        self.assertEqual(sg.repeat_for(overrides, "vm", "fib", "suite/fib"), 6)
        self.assertEqual(sg.repeat_for(overrides, "default", "sieve"), 8)
        self.assertIsNone(sg.repeat_for(overrides, "vm", "sieve"))

    def test_discovery_includes_all_groups(self):
        root = Path(__file__).resolve().parents[1]
        specs = sg.discover_specs(root, ["suite", "extended", "variants"])
        ids = {spec.benchmark_id for spec in specs}
        self.assertIn("suite/fib", ids)
        self.assertIn("suite/matmul_dense", ids)
        self.assertIn("extended/json_table_walk", ids)
        self.assertIn("variants/matmul_row_variant", ids)


if __name__ == "__main__":
    unittest.main()
