# Extended Benchmarks

These benchmarks are intentionally outside the 22-file core suite. They are
longer, application-shaped workloads meant to expose runtime gaps that direct
kernels can miss: mixed table shapes, nested field traversal, string parsing,
closure or method dispatch, coroutine yield/resume costs, and numeric work
interleaved with table mutation.

Run all GScript extended benchmarks:

```sh
bash benchmarks/extended/run_all.sh
```

Run a single benchmark manually:

```sh
go build -o /tmp/gscript_extended ./cmd/gscript
/tmp/gscript_extended -vm benchmarks/extended/json_table_walk.gs
/tmp/gscript_extended -jit benchmarks/extended/json_table_walk.gs
```

Run LuaJIT references:

```sh
luajit benchmarks/lua_extended/run_all.lua
```

Every benchmark prints a stable `checksum:` line and a final `Time: ...s` line.
The checksum is part of the benchmark contract: it prevents implementations from
silently skipping work and makes VM, JIT, and LuaJIT runs easy to compare.

## Manifest

| Benchmark | Runtime surfaces | Why it exists |
| --- | --- | --- |
| `json_table_walk` | JSON-like table construction, nested field access, array traversal, table mutation | Models decoded API payloads or document lists where hot loops repeatedly inspect nested records with a few stable shapes. |
| `log_tokenize_format` | `string.format`, `string.split`, `string.sub`, `tonumber`, table writes | Models log ingestion and metric extraction, where string library overhead and temporary tables dominate. |
| `actors_dispatch_mutation` | Function values stored in tables, indirect calls, polymorphic table shapes, field mutation | Models entity or job systems where each object carries behavior and mutable state. |
| `groupby_nested_agg` | Dynamic string keys, nested map creation, aggregate table mutation | Models analytics-style group-by accumulation over event streams. |
| `producer_consumer_pipeline` | Coroutine create/resume/yield, table payload transfer, consumer-side aggregation | Models generator pipelines and streaming transforms, not just raw coroutine ping-pong. |
| `mixed_inventory_sim` | Dense arrays, keyed tables, numeric scoring, periodic string formatting | Models business-logic loops where numeric work is mixed with object lookup, mutation, and reporting. |

