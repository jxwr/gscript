//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sort"
	"strings"
)

// LoopDiagnostic summarizes per-loop IR pressure that is otherwise hard to
// infer from a flat native instruction histogram.
type LoopDiagnostic struct {
	HeaderBlock      int                 `json:"header_block"`
	Blocks           []int               `json:"blocks"`
	Ops              LoopOpCounts        `json:"ops"`
	HeaderClobbers   []LoopValuePressure `json:"header_clobbers,omitempty"`
	InvariantReloads []LoopValuePressure `json:"invariant_reloads,omitempty"`
	Notes            []string            `json:"notes,omitempty"`
}

// LoopOpCounts counts operations in the loop body that tend to dominate tight
// numeric loops once generic dispatch has been removed.
type LoopOpCounts struct {
	IntArith          int `json:"int_arith"`
	IntArithChecked   int `json:"int_arith_checked"`
	FloatAdd          int `json:"float_add"`
	FloatMul          int `json:"float_mul"`
	FloatDiv          int `json:"float_div"`
	FMA               int `json:"fma"`
	TableArrayLoad    int `json:"table_array_load"`
	GenericSetTable   int `json:"generic_set_table"`
	ResidualCall      int `json:"residual_call"`
	PotentialBounds   int `json:"potential_bounds_checks"`
	LoopHeaderPhi     int `json:"loop_header_phi"`
	LoopCarriedFloats int `json:"loop_carried_floats"`
}

// LoopValuePressure records a value that cannot stay resident across the loop
// path because another loop-body value uses its assigned physical register.
type LoopValuePressure struct {
	ValueID         int    `json:"value_id"`
	Op              string `json:"op"`
	Type            string `json:"type"`
	Register        string `json:"register"`
	UseBlock        int    `json:"use_block,omitempty"`
	UseValueID      int    `json:"use_value_id,omitempty"`
	UseOp           string `json:"use_op,omitempty"`
	ClobberBlock    int    `json:"clobber_block,omitempty"`
	ClobberValueID  int    `json:"clobber_value_id,omitempty"`
	ClobberOp       string `json:"clobber_op,omitempty"`
	ClobberRegister string `json:"clobber_register,omitempty"`
	Reason          string `json:"reason"`
}

// BuildLoopDiagnostics constructs loop diagnostics from the optimized IR and
// final register allocation used by the production compiler.
func BuildLoopDiagnostics(fn *Function, alloc *RegAllocation) []LoopDiagnostic {
	if fn == nil || alloc == nil {
		return nil
	}
	li := computeLoopInfo(fn)
	if !li.hasLoops() {
		return nil
	}

	defs, defBlocks := collectInstrDefs(fn)
	uses := collectUses(fn)
	crossBlockLive := computeCrossBlockLive(fn)
	headerRegs := li.computeHeaderExitRegs(fn, alloc)
	headerFPRegs := li.computeHeaderExitFPRegs(fn, alloc)
	safeHdrRegs := computeSafeHeaderRegs(fn, li, alloc, headerRegs)
	safeHdrFPRegs := computeSafeHeaderFPRegs(fn, li, alloc, headerFPRegs)

	headers := make([]int, 0, len(li.loopHeaders))
	for headerID := range li.loopHeaders {
		headers = append(headers, headerID)
	}
	sort.Ints(headers)

	out := make([]LoopDiagnostic, 0, len(headers))
	for _, headerID := range headers {
		body := li.headerBlocks[headerID]
		diag := LoopDiagnostic{
			HeaderBlock: headerID,
			Blocks:      sortedBlockIDs(body),
			Ops:         countLoopOps(fn, body),
		}
		diag.HeaderClobbers = headerClobberDiagnostics(fn, alloc, body, headerID, defs,
			crossBlockLive, headerRegs, headerFPRegs, safeHdrRegs, safeHdrFPRegs)
		diag.InvariantReloads = invariantReloadDiagnostics(fn, alloc, body, defs, defBlocks, uses)
		if diag.Ops.FloatDiv > 0 {
			diag.Notes = append(diag.Notes, fmt.Sprintf("%d FloatDiv op(s) remain in the loop body", diag.Ops.FloatDiv))
		}
		if diag.Ops.TableArrayLoad > 0 {
			diag.Notes = append(diag.Notes, "typed array loads still carry per-load bounds checks")
		}
		if len(diag.HeaderClobbers)+len(diag.InvariantReloads) > 0 {
			diag.Notes = append(diag.Notes, "register pressure forces loop values through the VM frame")
		}
		out = append(out, diag)
	}
	return out
}

type loopUse struct {
	blockID int
	instr   *Instr
}

func collectInstrDefs(fn *Function) (map[int]*Instr, map[int]int) {
	defs := make(map[int]*Instr)
	blocks := make(map[int]int)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			if instr.Op.IsTerminator() {
				continue
			}
			defs[instr.ID] = instr
			blocks[instr.ID] = block.ID
		}
	}
	return defs, blocks
}

func collectUses(fn *Function) map[int][]loopUse {
	uses := make(map[int][]loopUse)
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			for _, arg := range instr.Args {
				if arg == nil {
					continue
				}
				uses[arg.ID] = append(uses[arg.ID], loopUse{blockID: block.ID, instr: instr})
			}
		}
	}
	return uses
}

func countLoopOps(fn *Function, blocks map[int]bool) LoopOpCounts {
	var counts LoopOpCounts
	for _, block := range fn.Blocks {
		if !blocks[block.ID] {
			continue
		}
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpPhi:
				counts.LoopHeaderPhi++
				if instr.Type == TypeFloat {
					counts.LoopCarriedFloats++
				}
			case OpAddInt, OpSubInt, OpMulInt, OpNegInt:
				counts.IntArith++
				if fn.Int48Safe == nil || !fn.Int48Safe[instr.ID] {
					counts.IntArithChecked++
				}
			case OpAddFloat, OpSubFloat:
				counts.FloatAdd++
			case OpMulFloat:
				counts.FloatMul++
			case OpDivFloat:
				counts.FloatDiv++
			case OpFMA, OpFMSUB:
				counts.FMA++
			case OpTableArrayLoad:
				counts.TableArrayLoad++
				counts.PotentialBounds++
			case OpSetTable:
				counts.GenericSetTable++
			case OpCall:
				counts.ResidualCall++
			}
		}
	}
	return counts
}

func headerClobberDiagnostics(fn *Function, alloc *RegAllocation, body map[int]bool, headerID int,
	defs map[int]*Instr, crossBlockLive map[int]bool,
	headerRegs map[int]map[int]loopRegEntry, headerFPRegs map[int]map[int]loopFPRegEntry,
	safeHdrRegs map[int]map[int]loopRegEntry, safeHdrFPRegs map[int]map[int]loopFPRegEntry) []LoopValuePressure {
	var out []LoopValuePressure
	for reg, entry := range headerRegs[headerID] {
		if _, ok := safeHdrRegs[headerID][reg]; ok || !crossBlockLive[entry.ValueID] {
			continue
		}
		clobber := firstRegisterClobber(fn, alloc, body, headerID, reg, false)
		out = append(out, loopPressure(entry.ValueID, defs[entry.ValueID], regName(reg, false), clobber,
			"loop-header GPR value is clobbered in the loop body"))
	}
	for reg, entry := range headerFPRegs[headerID] {
		if _, ok := safeHdrFPRegs[headerID][reg]; ok || !crossBlockLive[entry.ValueID] {
			continue
		}
		clobber := firstRegisterClobber(fn, alloc, body, headerID, reg, true)
		out = append(out, loopPressure(entry.ValueID, defs[entry.ValueID], regName(reg, true), clobber,
			"loop-header FPR value is clobbered in the loop body"))
	}
	limitLoopPressure(&out, 12)
	return out
}

func invariantReloadDiagnostics(fn *Function, alloc *RegAllocation, body map[int]bool,
	defs map[int]*Instr, defBlocks map[int]int, uses map[int][]loopUse) []LoopValuePressure {
	seen := make(map[int]bool)
	var out []LoopValuePressure
	for valID, valUses := range uses {
		defBlock, ok := defBlocks[valID]
		if !ok || body[defBlock] || seen[valID] {
			continue
		}
		pr, ok := alloc.ValueRegs[valID]
		if !ok || pr.IsFloat {
			continue
		}
		var firstUse loopUse
		usedInLoop := false
		for _, use := range valUses {
			if body[use.blockID] {
				firstUse = use
				usedInLoop = true
				break
			}
		}
		if !usedInLoop {
			continue
		}
		clobber := firstRegisterClobber(fn, alloc, body, -1, pr.Reg, false)
		if clobber == nil {
			continue
		}
		pressure := loopPressure(valID, defs[valID], regName(pr.Reg, false), clobber,
			"loop-invariant GPR value is used in the loop but its assigned register is clobbered")
		pressure.UseBlock = firstUse.blockID
		if firstUse.instr != nil {
			pressure.UseValueID = firstUse.instr.ID
			pressure.UseOp = firstUse.instr.Op.String()
		}
		out = append(out, pressure)
		seen[valID] = true
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UseBlock != out[j].UseBlock {
			return out[i].UseBlock < out[j].UseBlock
		}
		return out[i].ValueID < out[j].ValueID
	})
	limitLoopPressure(&out, 12)
	return out
}

func firstRegisterClobber(fn *Function, alloc *RegAllocation, body map[int]bool, skipBlockID, reg int, isFloat bool) *Instr {
	for _, block := range fn.Blocks {
		if !body[block.ID] || block.ID == skipBlockID {
			continue
		}
		for _, instr := range block.Instrs {
			if instr.Op == OpPhi || instr.Op.IsTerminator() {
				continue
			}
			pr, ok := alloc.ValueRegs[instr.ID]
			if !ok || pr.Reg != reg || pr.IsFloat != isFloat {
				continue
			}
			return instr
		}
	}
	return nil
}

func loopPressure(valueID int, def *Instr, reg string, clobber *Instr, reason string) LoopValuePressure {
	p := LoopValuePressure{
		ValueID:  valueID,
		Register: reg,
		Reason:   reason,
	}
	if def != nil {
		p.Op = def.Op.String()
		p.Type = def.Type.String()
	}
	if clobber != nil {
		p.ClobberBlock = clobber.Block.ID
		p.ClobberValueID = clobber.ID
		p.ClobberOp = clobber.Op.String()
		p.ClobberRegister = reg
	}
	return p
}

func sortedBlockIDs(blocks map[int]bool) []int {
	out := make([]int, 0, len(blocks))
	for id := range blocks {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func limitLoopPressure(items *[]LoopValuePressure, limit int) {
	if len(*items) > limit {
		*items = (*items)[:limit]
	}
}

func regName(reg int, isFloat bool) string {
	if isFloat {
		return fmt.Sprintf("D%d", reg)
	}
	return fmt.Sprintf("X%d", reg)
}

func FormatLoopDiagnostics(diags []LoopDiagnostic) string {
	if len(diags) == 0 {
		return "(no loops)\n"
	}
	var b strings.Builder
	for _, diag := range diags {
		fmt.Fprintf(&b, "loop header B%d blocks=%v\n", diag.HeaderBlock, diag.Blocks)
		fmt.Fprintf(&b, "  ops: int_arith=%d checked=%d float_add=%d float_mul=%d float_div=%d fma=%d table_array_load=%d bounds=%d settable=%d call=%d phis=%d float_phis=%d\n",
			diag.Ops.IntArith, diag.Ops.IntArithChecked, diag.Ops.FloatAdd,
			diag.Ops.FloatMul, diag.Ops.FloatDiv, diag.Ops.FMA,
			diag.Ops.TableArrayLoad, diag.Ops.PotentialBounds,
			diag.Ops.GenericSetTable, diag.Ops.ResidualCall,
			diag.Ops.LoopHeaderPhi, diag.Ops.LoopCarriedFloats)
		writeLoopPressures(&b, "  header clobbers", diag.HeaderClobbers)
		writeLoopPressures(&b, "  invariant reloads", diag.InvariantReloads)
		for _, note := range diag.Notes {
			fmt.Fprintf(&b, "  note: %s\n", note)
		}
	}
	return b.String()
}

func writeLoopPressures(b *strings.Builder, label string, items []LoopValuePressure) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n", label)
	for _, item := range items {
		use := ""
		if item.UseOp != "" {
			use = fmt.Sprintf(" used_by=B%d/v%d %s", item.UseBlock, item.UseValueID, item.UseOp)
		}
		clobber := ""
		if item.ClobberOp != "" {
			clobber = fmt.Sprintf(" clobbered_by=B%d/v%d %s", item.ClobberBlock, item.ClobberValueID, item.ClobberOp)
		}
		fmt.Fprintf(b, "    v%d %s:%s %s%s%s -- %s\n",
			item.ValueID, item.Op, item.Type, item.Register, use, clobber, item.Reason)
	}
}
