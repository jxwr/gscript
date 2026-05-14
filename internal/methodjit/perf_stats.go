//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gscript/gscript/internal/vm"
)

const (
	perfTier2NativeExecution          = "tier2_native_execution"
	perfTier2ExitResume               = "tier2_exit_resume"
	perfTier2TableExit                = "tier2_table_exit"
	perfTier2OpExit                   = "tier2_op_exit"
	perfTier2NativeCallExitProtocol   = "tier2_native_call_exit_protocol"
	perfTier2CompiledProtocol         = "tier2_compiled_protocol"
	perfTier2CompiledProtocolCallExit = "tier2_compiled_protocol_call_exit"
)

type tier2PerfMark struct {
	enabled bool
	start   time.Time
}

type tier2PerfCounters struct {
	Count uint64
	Nanos uint64
}

type tier2PerfStatsCollector struct {
	enabled atomic.Bool
	mu      sync.Mutex
	rows    map[string]tier2PerfCounters
}

// Tier2PerfStatsRow is one aggregated low-level Tier 2 timing/counter row.
type Tier2PerfStatsRow struct {
	Name     string `json:"name"`
	Count    uint64 `json:"count"`
	Nanos    uint64 `json:"nanos"`
	AvgNanos uint64 `json:"avg_nanos"`
}

// Tier2BlockCounterMeta describes one emitted Tier 2 basic-block counter.
type Tier2BlockCounterMeta struct {
	Proto    string   `json:"proto"`
	BlockID  int      `json:"block_id"`
	InstrIDs []int    `json:"instr_ids,omitempty"`
	Ops      []string `json:"ops,omitempty"`
}

// Tier2BlockPerfRow is a native block-entry counter row for opt-in Tier 2
// diagnostics. Counts are approximate if the same compiled function is entered
// concurrently, which is acceptable for profiling attribution.
type Tier2BlockPerfRow struct {
	Proto    string   `json:"proto"`
	BlockID  int      `json:"block_id"`
	Count    uint64   `json:"count"`
	InstrIDs []int    `json:"instr_ids,omitempty"`
	Ops      []string `json:"ops,omitempty"`
}

// Tier2CallCounterMeta describes one emitted Tier 2 call-site path counter.
// Kind is a stable route such as field_call_floor; Outcome is success,
// fallback, or exit.
type Tier2CallCounterMeta struct {
	Proto   string `json:"proto"`
	InstrID int    `json:"instr_id"`
	Op      string `json:"op"`
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
}

// Tier2CallPerfRow is a native call-site path counter row for opt-in Tier 2
// diagnostics. It lets benchmarks distinguish successful JIT-to-JIT calls from
// Go fallback/exit recovery without instrumenting normal production code.
type Tier2CallPerfRow struct {
	Proto   string `json:"proto"`
	InstrID int    `json:"instr_id"`
	Op      string `json:"op"`
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
	Count   uint64 `json:"count"`
}

// Tier2PerfStatsSnapshot is a stable, JSON-friendly diagnostic snapshot.
type Tier2PerfStatsSnapshot struct {
	Enabled bool                `json:"enabled"`
	Rows    []Tier2PerfStatsRow `json:"rows"`
	Blocks  []Tier2BlockPerfRow `json:"blocks,omitempty"`
	Calls   []Tier2CallPerfRow  `json:"calls,omitempty"`
}

func (s *tier2PerfStatsCollector) setEnabled(enabled bool) {
	s.enabled.Store(enabled)
}

func (s *tier2PerfStatsCollector) isEnabled() bool {
	return s.enabled.Load()
}

func (s *tier2PerfStatsCollector) record(name string, d time.Duration) {
	if name == "" || d < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows == nil {
		s.rows = make(map[string]tier2PerfCounters)
	}
	row := s.rows[name]
	row.Count++
	row.Nanos += uint64(d.Nanoseconds())
	s.rows[name] = row
}

func (s *tier2PerfStatsCollector) snapshot() Tier2PerfStatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := Tier2PerfStatsSnapshot{
		Enabled: s.enabled.Load(),
		Rows:    make([]Tier2PerfStatsRow, 0, len(s.rows)),
	}
	for name, row := range s.rows {
		avg := uint64(0)
		if row.Count != 0 {
			avg = row.Nanos / row.Count
		}
		out.Rows = append(out.Rows, Tier2PerfStatsRow{
			Name:     name,
			Count:    row.Count,
			Nanos:    row.Nanos,
			AvgNanos: avg,
		})
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		return out.Rows[i].Name < out.Rows[j].Name
	})
	return out
}

// EnableTier2PerfStats enables opt-in Tier 2 protocol/timing diagnostics.
func (tm *TieringManager) EnableTier2PerfStats() {
	if tm == nil {
		return
	}
	if tm.perfStats == nil {
		tm.perfStats = &tier2PerfStatsCollector{}
	}
	tm.perfStatsEnabled = true
	tm.perfStats.setEnabled(true)
}

// DisableTier2PerfStats disables Tier 2 protocol/timing diagnostics.
func (tm *TieringManager) DisableTier2PerfStats() {
	if tm == nil || tm.perfStats == nil {
		return
	}
	tm.perfStatsEnabled = false
	tm.perfStats.setEnabled(false)
}

func (tm *TieringManager) tier2PerfStart() tier2PerfMark {
	if tm == nil || !tm.perfStatsEnabled || tm.perfStats == nil {
		return tier2PerfMark{}
	}
	return tier2PerfMark{enabled: true, start: time.Now()}
}

func (tm *TieringManager) tier2PerfStop(name string, mark tier2PerfMark) {
	if tm == nil || tm.perfStats == nil || !mark.enabled {
		return
	}
	tm.perfStats.record(name, time.Since(mark.start))
}

// Tier2PerfStats returns the current opt-in Tier 2 performance diagnostic
// counters. When disabled, the snapshot still reports Enabled=false.
func (tm *TieringManager) Tier2PerfStats() Tier2PerfStatsSnapshot {
	if tm == nil || tm.perfStats == nil {
		return Tier2PerfStatsSnapshot{}
	}
	snap := tm.perfStats.snapshot()
	snap.Blocks = tm.tier2BlockPerfRows()
	snap.Calls = tm.tier2CallPerfRows()
	return snap
}

func (tm *TieringManager) tier2BlockPerfRows() []Tier2BlockPerfRow {
	if tm == nil {
		return nil
	}
	rows := make([]Tier2BlockPerfRow, 0)
	tm.forEachTier2Compiled(func(_proto *vm.FuncProto, cf *CompiledFunction) {
		if cf == nil || len(cf.Tier2BlockCounters) == 0 {
			return
		}
		for i, count := range cf.Tier2BlockCounters {
			if count == 0 {
				continue
			}
			meta := Tier2BlockCounterMeta{}
			if i < len(cf.Tier2BlockCounterMeta) {
				meta = cf.Tier2BlockCounterMeta[i]
			}
			rows = append(rows, Tier2BlockPerfRow{
				Proto:    meta.Proto,
				BlockID:  meta.BlockID,
				Count:    count,
				InstrIDs: append([]int(nil), meta.InstrIDs...),
				Ops:      append([]string(nil), meta.Ops...),
			})
		}
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		if rows[i].Proto != rows[j].Proto {
			return rows[i].Proto < rows[j].Proto
		}
		return rows[i].BlockID < rows[j].BlockID
	})
	return rows
}

func (tm *TieringManager) tier2CallPerfRows() []Tier2CallPerfRow {
	if tm == nil {
		return nil
	}
	rows := make([]Tier2CallPerfRow, 0)
	tm.forEachTier2Compiled(func(_proto *vm.FuncProto, cf *CompiledFunction) {
		if cf == nil || len(cf.Tier2CallCounters) == 0 {
			return
		}
		for i, count := range cf.Tier2CallCounters {
			if count == 0 {
				continue
			}
			meta := Tier2CallCounterMeta{}
			if i < len(cf.Tier2CallCounterMeta) {
				meta = cf.Tier2CallCounterMeta[i]
			}
			rows = append(rows, Tier2CallPerfRow{
				Proto:   meta.Proto,
				InstrID: meta.InstrID,
				Op:      meta.Op,
				Kind:    meta.Kind,
				Outcome: meta.Outcome,
				Count:   count,
			})
		}
	})
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		if rows[i].Proto != rows[j].Proto {
			return rows[i].Proto < rows[j].Proto
		}
		if rows[i].InstrID != rows[j].InstrID {
			return rows[i].InstrID < rows[j].InstrID
		}
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Outcome < rows[j].Outcome
	})
	return rows
}

// WriteTier2PerfStatsText prints Tier 2 protocol/timing diagnostics in a stable
// text form. Durations are inclusive for the named phase.
func (tm *TieringManager) WriteTier2PerfStatsText(w io.Writer) {
	snap := tm.Tier2PerfStats()
	fmt.Fprintln(w, "Tier 2 Performance Diagnostics:")
	fmt.Fprintf(w, "  enabled: %v\n", snap.Enabled)
	fmt.Fprintln(w, "  rows:")
	for _, row := range snap.Rows {
		fmt.Fprintf(w, "    %s: count=%d total=%dns avg=%dns\n",
			row.Name, row.Count, row.Nanos, row.AvgNanos)
	}
	if len(snap.Blocks) > 0 {
		fmt.Fprintln(w, "  blocks:")
		for _, row := range snap.Blocks {
			fmt.Fprintf(w, "    %s B%d: count=%d ops=%v\n", row.Proto, row.BlockID, row.Count, row.Ops)
		}
	}
	if len(snap.Calls) > 0 {
		fmt.Fprintln(w, "  calls:")
		for _, row := range snap.Calls {
			fmt.Fprintf(w, "    %s #%d %s/%s/%s: count=%d\n",
				row.Proto, row.InstrID, row.Op, row.Kind, row.Outcome, row.Count)
		}
	}
}
