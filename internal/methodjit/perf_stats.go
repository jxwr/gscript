//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"sync/atomic"
	"time"
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

// Tier2PerfStatsSnapshot is a stable, JSON-friendly diagnostic snapshot.
type Tier2PerfStatsSnapshot struct {
	Enabled bool                `json:"enabled"`
	Rows    []Tier2PerfStatsRow `json:"rows"`
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
	tm.perfStats.setEnabled(true)
}

// DisableTier2PerfStats disables Tier 2 protocol/timing diagnostics.
func (tm *TieringManager) DisableTier2PerfStats() {
	if tm == nil {
		return
	}
	tm.perfStats.setEnabled(false)
}

func (tm *TieringManager) tier2PerfStart() tier2PerfMark {
	if tm == nil || !tm.perfStats.isEnabled() {
		return tier2PerfMark{}
	}
	return tier2PerfMark{enabled: true, start: time.Now()}
}

func (tm *TieringManager) tier2PerfStop(name string, mark tier2PerfMark) {
	if tm == nil || !mark.enabled {
		return
	}
	tm.perfStats.record(name, time.Since(mark.start))
}

// Tier2PerfStats returns the current opt-in Tier 2 performance diagnostic
// counters. When disabled, the snapshot still reports Enabled=false.
func (tm *TieringManager) Tier2PerfStats() Tier2PerfStatsSnapshot {
	if tm == nil {
		return Tier2PerfStatsSnapshot{}
	}
	return tm.perfStats.snapshot()
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
}
