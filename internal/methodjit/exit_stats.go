//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/gscript/gscript/internal/vm"
)

// ExitSiteMeta ties a Tier 2 exit-resume/deopt instruction ID back to the
// optimized IR operation and source bytecode PC that produced it.
type ExitSiteMeta struct {
	PC     int
	Op     string
	Reason string
}

type exitStatsKey struct {
	proto    string
	code     int
	opID     int
	pc       int
	reason   string
	exitName string
}

type exitStatsCollector struct {
	mu     sync.Mutex
	total  uint64
	byCode map[int]uint64
	sites  map[exitStatsKey]uint64
}

// ExitStatsSite is one aggregated exit/deopt profile row.
type ExitStatsSite struct {
	Proto    string `json:"proto"`
	ExitCode int    `json:"exit_code"`
	ExitName string `json:"exit_name"`
	OpID     int    `json:"op_id"`
	PC       int    `json:"pc"`
	Reason   string `json:"reason"`
	Count    uint64 `json:"count"`
}

// ExitStatsSnapshot is a stable, JSON-friendly copy of collected Tier 2 exits.
type ExitStatsSnapshot struct {
	Total      uint64            `json:"total"`
	ByExitCode map[string]uint64 `json:"by_exit_code"`
	Sites      []ExitStatsSite   `json:"sites"`
}

func buildExitSiteMeta(fn *Function) map[int]ExitSiteMeta {
	if fn == nil {
		return nil
	}
	sites := make(map[int]ExitSiteMeta)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr == nil {
				continue
			}
			reason := instr.Op.String()
			if instr.Op == OpGuardType {
				reason = fmt.Sprintf("GuardType(%s)", Type(instr.Aux))
			} else if instr.Op == OpNewTable {
				reason = newTableExitReason(instr)
			} else if instr.Op == OpNewFixedTable {
				reason = fmt.Sprintf("NewFixedTable(fields=%d,ctor=%d)", instr.Aux2, instr.Aux)
			}
			pc := -1
			if instr.HasSource {
				pc = instr.SourcePC
			}
			sites[instr.ID] = ExitSiteMeta{
				PC:     pc,
				Op:     instr.Op.String(),
				Reason: reason,
			}
		}
	}
	return sites
}

func (tm *TieringManager) recordTier2Exit(proto *vm.FuncProto, cf *CompiledFunction, ctx *ExecContext) {
	if ctx == nil {
		return
	}
	switch ctx.ExitCode {
	case ExitDeopt, ExitCallExit, ExitGlobalExit, ExitTableExit, ExitOpExit:
	default:
		return
	}

	protoName := "<nil>"
	if proto != nil {
		protoName = proto.Name
		if protoName == "" {
			protoName = "<anonymous>"
		}
	}

	opID := exitStatsOpID(ctx)
	pc := -1
	reason := exitStatsReason(ctx)
	if cf != nil && cf.ExitSites != nil {
		if meta, ok := cf.ExitSites[opID]; ok {
			pc = meta.PC
			if meta.Reason != "" {
				if ctx.ExitCode == ExitDeopt {
					reason = "deopt:" + meta.Reason
				} else {
					reason = meta.Reason
				}
			}
		}
	}

	tm.exitStats.record(exitStatsKey{
		proto:    protoName,
		code:     int(ctx.ExitCode),
		opID:     opID,
		pc:       pc,
		reason:   reason,
		exitName: exitCodeName(int(ctx.ExitCode)),
	})
}

func (s *exitStatsCollector) record(key exitStatsKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byCode == nil {
		s.byCode = make(map[int]uint64)
	}
	if s.sites == nil {
		s.sites = make(map[exitStatsKey]uint64)
	}
	s.total++
	s.byCode[key.code]++
	s.sites[key]++
}

func (s *exitStatsCollector) snapshot() ExitStatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := ExitStatsSnapshot{
		Total:      s.total,
		ByExitCode: make(map[string]uint64, len(s.byCode)),
		Sites:      make([]ExitStatsSite, 0, len(s.sites)),
	}
	for code, count := range s.byCode {
		out.ByExitCode[exitCodeName(code)] = count
	}
	for key, count := range s.sites {
		out.Sites = append(out.Sites, ExitStatsSite{
			Proto:    key.proto,
			ExitCode: key.code,
			ExitName: key.exitName,
			OpID:     key.opID,
			PC:       key.pc,
			Reason:   key.reason,
			Count:    count,
		})
	}
	sort.Slice(out.Sites, func(i, j int) bool {
		a, b := out.Sites[i], out.Sites[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.Proto != b.Proto {
			return a.Proto < b.Proto
		}
		if a.ExitCode != b.ExitCode {
			return a.ExitCode < b.ExitCode
		}
		if a.PC != b.PC {
			return a.PC < b.PC
		}
		if a.OpID != b.OpID {
			return a.OpID < b.OpID
		}
		return a.Reason < b.Reason
	})
	return out
}

// ExitStats returns the production Tier 2 exit/deopt profile collected by the
// real executeTier2 exit handler path.
func (tm *TieringManager) ExitStats() ExitStatsSnapshot {
	if tm == nil {
		return ExitStatsSnapshot{ByExitCode: map[string]uint64{}}
	}
	return tm.exitStats.snapshot()
}

// WriteExitStatsText prints the Tier 2 exit/deopt profile in a stable text form.
func (tm *TieringManager) WriteExitStatsText(w io.Writer) {
	snap := tm.ExitStats()
	fmt.Fprintln(w, "Tier 2 Exit Profile:")
	fmt.Fprintf(w, "  total exits: %d\n", snap.Total)
	fmt.Fprintln(w, "  by exit code:")
	names := make([]string, 0, len(snap.ByExitCode))
	for name := range snap.ByExitCode {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "    %s: %d\n", name, snap.ByExitCode[name])
	}
	fmt.Fprintln(w, "  sites:")
	for _, site := range snap.Sites {
		fmt.Fprintf(w, "    %d  proto=%s exit=%s id=%d pc=%d reason=%s\n",
			site.Count, site.Proto, site.ExitName, site.OpID, site.PC, site.Reason)
	}
}

func exitStatsOpID(ctx *ExecContext) int {
	switch ctx.ExitCode {
	case ExitDeopt:
		return int(ctx.DeoptInstrID)
	case ExitCallExit:
		return int(ctx.CallID)
	case ExitGlobalExit:
		return int(ctx.GlobalExitID)
	case ExitTableExit:
		return int(ctx.TableExitID)
	case ExitOpExit:
		return int(ctx.OpExitID)
	default:
		return 0
	}
}

func exitStatsReason(ctx *ExecContext) string {
	switch ctx.ExitCode {
	case ExitDeopt:
		return "deopt"
	case ExitCallExit:
		return "Call"
	case ExitGlobalExit:
		return "GetGlobal"
	case ExitTableExit:
		return tableOpName(int(ctx.TableOp))
	case ExitOpExit:
		return Op(ctx.OpExitOp).String()
	default:
		return "unknown"
	}
}

func exitCodeName(code int) string {
	switch code {
	case ExitDeopt:
		return "ExitDeopt"
	case ExitCallExit:
		return "ExitCallExit"
	case ExitGlobalExit:
		return "ExitGlobalExit"
	case ExitTableExit:
		return "ExitTableExit"
	case ExitOpExit:
		return "ExitOpExit"
	default:
		return fmt.Sprintf("ExitCode%d", code)
	}
}

func tableOpName(op int) string {
	switch op {
	case TableOpNewTable:
		return "NewTable"
	case TableOpGetTable:
		return "GetTable"
	case TableOpSetTable:
		return "SetTable"
	case TableOpGetField:
		return "GetField"
	case TableOpSetField:
		return "SetField"
	case TableOpNewFixedTable2:
		return "NewFixedTable2"
	default:
		return fmt.Sprintf("TableOp%d", op)
	}
}
