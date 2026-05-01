#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
pattern="${1:-^BenchmarkPrecision}"
if (($# > 0)); then
  shift
fi

benchtime="${BENCHTIME:-500ms}"
count="${COUNT:-1}"

cd "$repo_root"
go test ./benchmarks -run '^$' -bench "$pattern" -benchtime "$benchtime" -count "$count" -benchmem "$@"
