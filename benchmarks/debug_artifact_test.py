import json
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import debug_artifact as da


class DebugArtifactTest(unittest.TestCase):
    def test_aggregates_existing_outputs(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            timing = root / "timing.json"
            timing.write_text(
                json.dumps(
                    {
                        "results": [
                            {
                                "group": "suite",
                                "benchmark": "fib",
                                "modes": {
                                    "default": {
                                        "current": {
                                            "status": "ok",
                                            "source": "script",
                                            "repeat": 4,
                                            "stats": {"median": 0.01},
                                            "t2_entered": 1,
                                            "exit_total": 2,
                                        }
                                    }
                                },
                            }
                        ]
                    }
                )
            )
            exits = root / "exits.json"
            exits.write_text(
                json.dumps(
                    {
                        "results": [
                            {
                                "benchmark": "fib",
                                "status": "ok",
                                "stats": {
                                    "by_exit_code": {"ExitDeopt": 3},
                                    "sites": [{"count": 3, "reason": "deopt:GuardType"}],
                                },
                            }
                        ]
                    }
                )
            )
            runtime = root / "runtime.txt"
            runtime.write_text(
                """Runtime Path Statistics:
  native_call:
    fast: 7
    fallback: 1
"""
            )
            perf = root / "perf.txt"
            perf.write_text(
                """Tier 2 Performance Diagnostics:
  enabled: true
  rows:
    tier2_native_execution: count=2 total=100ns avg=50ns
"""
            )
            spec = root / "spec.json"
            spec.write_text(
                json.dumps(
                    [
                        {
                            "proto_name": "fib",
                            "compiled": True,
                            "version_hash": "abc",
                            "guard_count": 3,
                            "suppressed_count": 2,
                            "suppressed_pcs": [4, 9],
                        }
                    ]
                )
            )
            warm = root / "warm"
            warm.mkdir()
            (warm / "manifest.json").write_text(
                json.dumps({"protos": [{"name": "fib", "status": "entered", "entered": True, "compiled": True, "code_bytes": 32}]})
            )
            (warm / "pcmap.json").write_text(json.dumps({"functions": [{"ranges": [{}, {}]}]}))

            args = type(
                "Args",
                (),
                {
                    "benchmark_json": [timing],
                    "exit_stats": exits,
                    "runtime_path_stats": runtime,
                    "perf_stats": perf,
                    "spec_state": spec,
                    "warm_dump": warm,
                    "label": "unit",
                },
            )()
            artifact = da.build_artifact(args, root)

        self.assertEqual(artifact["schema_version"], 1)
        self.assertEqual(artifact["benchmark_summary"]["rows"], 1)
        self.assertEqual(artifact["benchmark_summary"]["total_exits"], 2)
        self.assertEqual(artifact["debug"]["exit_stats"]["total"], 3)
        self.assertEqual(artifact["debug"]["runtime_path_stats"]["numbers"]["native_call.fast"], 7.0)
        self.assertEqual(artifact["debug"]["tier2_perf_stats"]["total_nanos"], 100)
        self.assertEqual(artifact["debug"]["tier2_speculation_state"]["suppressed"], 2)
        self.assertEqual(artifact["specialization"]["compiled"], 1)
        self.assertEqual(artifact["debug"]["warm_dump"]["pcmap_ranges"], 2)
        self.assertEqual(artifact["timing"]["summary"]["rows"], 1)
        self.assertEqual(artifact["tiering"]["t2_entered"], 1)
        self.assertEqual(artifact["exits"]["total"], 3)
        self.assertEqual(artifact["runtime_paths"]["numbers"]["native_call.fast"], 7.0)
        self.assertEqual(artifact["profiles"]["pcmap_ranges"], 2)
        self.assertEqual(artifact["gates"]["reason_counts"]["deopt:GuardType"], 3)


if __name__ == "__main__":
    unittest.main()
