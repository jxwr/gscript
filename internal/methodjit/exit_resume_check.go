//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"os"
	"sort"
	"unsafe"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

const exitResumeCheckEnv = "GSCRIPT_EXIT_RESUME_CHECK"

type exitResumeCheckKey struct {
	ExitCode    int64
	InstrID     int
	NumericPass bool
}

type exitResumeLiveSlot struct {
	ValueID  int
	Slot     int
	RawInt   bool
	RawFloat bool
}

type exitResumeCheckSite struct {
	Key                exitResumeCheckKey
	LiveSlots          []exitResumeLiveSlot
	ModifiedSlots      map[int]bool
	RequireCallFunc    bool
	RequireRawIntArgs  bool
	RequireTableInputs bool
}

type exitResumeCheckMetadata struct {
	Sites map[exitResumeCheckKey]*exitResumeCheckSite
}

type exitResumeCheckState struct {
	shadow []runtime.Value
}

func exitResumeCheckEnabled() bool {
	return os.Getenv(exitResumeCheckEnv) == "1"
}

func newExitResumeCheckMetadata() *exitResumeCheckMetadata {
	return &exitResumeCheckMetadata{Sites: make(map[exitResumeCheckKey]*exitResumeCheckSite)}
}

func newExitResumeCheckState(cf *CompiledFunction) *exitResumeCheckState {
	if cf == nil || cf.ExitResumeCheck == nil || cf.numRegs <= 0 {
		return nil
	}
	return &exitResumeCheckState{shadow: make([]runtime.Value, cf.numRegs)}
}

func (s *exitResumeCheckState) shadowPtr() uintptr {
	if s == nil || len(s.shadow) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&s.shadow[0]))
}

func (ec *emitContext) recordExitResumeCheckSite(instr *Instr, exitCode int64, modifiedSlots []int, opts exitResumeCheckOptions) {
	if ec.exitResumeCheck == nil || instr == nil {
		return
	}
	ec.recordExitResumeCheckSiteWithLive(instr, exitCode, ec.exitResumeCheckLiveSlots(ec.activeRegs, ec.activeFPRegs), modifiedSlots, opts)
}

func (ec *emitContext) recordExitResumeCheckSiteWithLive(instr *Instr, exitCode int64, live []exitResumeLiveSlot, modifiedSlots []int, opts exitResumeCheckOptions) {
	if ec.exitResumeCheck == nil || instr == nil {
		return
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].Slot == live[j].Slot {
			return live[i].ValueID < live[j].ValueID
		}
		return live[i].Slot < live[j].Slot
	})
	key := exitResumeCheckKey{ExitCode: exitCode, InstrID: instr.ID, NumericPass: ec.numericMode}
	site := &exitResumeCheckSite{
		Key:                key,
		LiveSlots:          live,
		ModifiedSlots:      make(map[int]bool, len(modifiedSlots)),
		RequireCallFunc:    opts.RequireCallFunc,
		RequireRawIntArgs:  opts.RequireRawIntArgs,
		RequireTableInputs: opts.RequireTableInputs,
	}
	for _, slot := range modifiedSlots {
		if slot >= 0 {
			site.ModifiedSlots[slot] = true
		}
	}
	ec.exitResumeCheck.Sites[key] = site
}

type exitResumeCheckOptions struct {
	RequireCallFunc    bool
	RequireRawIntArgs  bool
	RequireTableInputs bool
}

func callExitModifiedSlots(funcSlot, nRets int) []int {
	if nRets <= 0 {
		nRets = 1
	}
	out := make([]int, 0, nRets)
	for i := 0; i < nRets; i++ {
		out = append(out, funcSlot+i)
	}
	return out
}

func (ec *emitContext) exitResumeCheckLiveSlots(gprLive, fprLive map[int]bool) []exitResumeLiveSlot {
	if ec.exitResumeCheck == nil {
		return nil
	}
	live := make([]exitResumeLiveSlot, 0, len(gprLive)+len(fprLive))
	seen := make(map[int]bool, len(gprLive)+len(fprLive))
	for valueID := range gprLive {
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || pr.IsFloat {
			continue
		}
		slot, ok := ec.slotMap[valueID]
		if !ok {
			continue
		}
		live = append(live, exitResumeLiveSlot{
			ValueID: valueID,
			Slot:    slot,
			RawInt:  ec.rawIntRegs[valueID],
		})
		seen[valueID] = true
	}
	for valueID := range fprLive {
		if seen[valueID] {
			continue
		}
		pr, ok := ec.alloc.ValueRegs[valueID]
		if !ok || !pr.IsFloat {
			continue
		}
		slot, ok := ec.slotMap[valueID]
		if !ok {
			continue
		}
		live = append(live, exitResumeLiveSlot{
			ValueID:  valueID,
			Slot:     slot,
			RawFloat: true,
		})
	}
	return live
}

func (cf *CompiledFunction) exitResumeCheckSite(ctx *ExecContext) *exitResumeCheckSite {
	if cf == nil || cf.ExitResumeCheck == nil || ctx == nil {
		return nil
	}
	id := 0
	switch ctx.ExitCode {
	case ExitCallExit:
		id = int(ctx.CallID)
	case ExitGlobalExit:
		id = int(ctx.GlobalExitID)
	case ExitTableExit:
		id = int(ctx.TableExitID)
	case ExitOpExit:
		id = int(ctx.OpExitID)
	default:
		return nil
	}
	key := exitResumeCheckKey{ExitCode: ctx.ExitCode, InstrID: id, NumericPass: ctx.ResumeNumericPass != 0}
	return cf.ExitResumeCheck.Sites[key]
}

func (s *exitResumeCheckState) checkBefore(ctx *ExecContext, site *exitResumeCheckSite, regs []runtime.Value, base int, protoName string) (map[int]runtime.Value, error) {
	if s == nil || site == nil {
		return nil, nil
	}
	before := make(map[int]runtime.Value, len(site.LiveSlots))
	for _, live := range site.LiveSlots {
		abs := base + live.Slot
		if live.Slot < 0 || live.Slot >= len(s.shadow) {
			return nil, fmt.Errorf("exit-resume-check: %s instr=%d value=%d home slot %d outside shadow len %d",
				protoName, site.Key.InstrID, live.ValueID, live.Slot, len(s.shadow))
		}
		if abs < 0 || abs >= len(regs) {
			return nil, fmt.Errorf("exit-resume-check: %s instr=%d value=%d home slot %d abs %d outside regs len %d",
				protoName, site.Key.InstrID, live.ValueID, live.Slot, abs, len(regs))
		}
		home := regs[abs]
		shadow := s.shadow[live.Slot]
		if home != shadow {
			return nil, fmt.Errorf("exit-resume-check: %s instr=%d value=%d slot=%d materialization mismatch home=%016x shadow=%016x",
				protoName, site.Key.InstrID, live.ValueID, live.Slot, uint64(home), uint64(shadow))
		}
		if live.RawInt && !home.IsInt() {
			return nil, fmt.Errorf("exit-resume-check: %s instr=%d value=%d slot=%d raw-int materialized as non-int %016x",
				protoName, site.Key.InstrID, live.ValueID, live.Slot, uint64(home))
		}
		if live.RawFloat && !home.IsFloat() {
			return nil, fmt.Errorf("exit-resume-check: %s instr=%d value=%d slot=%d raw-float materialized as non-float %016x",
				protoName, site.Key.InstrID, live.ValueID, live.Slot, uint64(home))
		}
		before[live.Slot] = home
	}
	if err := s.checkExitDescriptor(ctx, site, regs, base, protoName); err != nil {
		return nil, err
	}
	return before, nil
}

func (s *exitResumeCheckState) checkAfter(site *exitResumeCheckSite, before map[int]runtime.Value, regs []runtime.Value, base int, protoName string) error {
	if s == nil || site == nil {
		return nil
	}
	for slot, want := range before {
		if site.ModifiedSlots[slot] {
			continue
		}
		abs := base + slot
		if abs < 0 || abs >= len(regs) {
			return fmt.Errorf("exit-resume-check: %s instr=%d slot=%d abs=%d outside regs len %d after fallback",
				protoName, site.Key.InstrID, slot, abs, len(regs))
		}
		if got := regs[abs]; got != want {
			return fmt.Errorf("exit-resume-check: %s instr=%d slot=%d live slot clobbered by fallback before=%016x after=%016x",
				protoName, site.Key.InstrID, slot, uint64(want), uint64(got))
		}
	}
	return nil
}

func (s *exitResumeCheckState) checkExitDescriptor(ctx *ExecContext, site *exitResumeCheckSite, regs []runtime.Value, base int, protoName string) error {
	switch site.Key.ExitCode {
	case ExitCallExit:
		callSlot := int(ctx.CallSlot)
		nArgs := int(ctx.CallNArgs)
		absCall := base + callSlot
		if callSlot < 0 || nArgs < 0 || absCall < 0 || absCall >= len(regs) {
			return fmt.Errorf("exit-resume-check: %s instr=%d invalid call frame callSlot=%d nArgs=%d regs=%d",
				protoName, site.Key.InstrID, callSlot, nArgs, len(regs))
		}
		if site.RequireCallFunc && !regs[absCall].IsFunction() {
			return fmt.Errorf("exit-resume-check: %s instr=%d call slot %d is not function: %016x",
				protoName, site.Key.InstrID, callSlot, uint64(regs[absCall]))
		}
		for i := 0; i < nArgs; i++ {
			absArg := absCall + 1 + i
			if absArg < 0 || absArg >= len(regs) {
				return fmt.Errorf("exit-resume-check: %s instr=%d arg %d abs slot %d outside regs len %d",
					protoName, site.Key.InstrID, i, absArg, len(regs))
			}
			if site.RequireRawIntArgs && !regs[absArg].IsInt() {
				return fmt.Errorf("exit-resume-check: %s instr=%d raw-int fallback arg %d slot %d materialized as %016x",
					protoName, site.Key.InstrID, i, callSlot+1+i, uint64(regs[absArg]))
			}
		}
	case ExitTableExit:
		if site.RequireTableInputs {
			return s.checkTableDescriptor(ctx, regs, base, protoName, site.Key.InstrID)
		}
	}
	return nil
}

func (s *exitResumeCheckState) checkTableDescriptor(ctx *ExecContext, regs []runtime.Value, base int, protoName string, instrID int) error {
	checkRel := func(slot int, label string) error {
		if slot < 0 {
			return fmt.Errorf("%s rel slot %d is negative", label, slot)
		}
		return checkSlotInRange(base+slot, regs, label)
	}
	switch ctx.TableOp {
	case TableOpNewTable:
		return checkRel(int(ctx.TableSlot), "newtable result")
	case TableOpGetTable:
		if err := checkRel(int(ctx.TableSlot), "gettable table"); err != nil {
			return fmt.Errorf("exit-resume-check: %s instr=%d %w", protoName, instrID, err)
		}
		if err := checkRel(int(ctx.TableKeySlot), "gettable key"); err != nil {
			return fmt.Errorf("exit-resume-check: %s instr=%d %w", protoName, instrID, err)
		}
		return checkRel(int(ctx.TableAux), "gettable result")
	case TableOpSetTable:
		if err := checkRel(int(ctx.TableSlot), "settable table"); err != nil {
			return fmt.Errorf("exit-resume-check: %s instr=%d %w", protoName, instrID, err)
		}
		if err := checkRel(int(ctx.TableKeySlot), "settable key"); err != nil {
			return fmt.Errorf("exit-resume-check: %s instr=%d %w", protoName, instrID, err)
		}
		return checkRel(int(ctx.TableValSlot), "settable value")
	case TableOpGetField:
		if err := checkRel(int(ctx.TableSlot), "getfield table"); err != nil {
			return fmt.Errorf("exit-resume-check: %s instr=%d %w", protoName, instrID, err)
		}
		return checkRel(int(ctx.TableAux2), "getfield result")
	case TableOpSetField:
		if err := checkRel(int(ctx.TableSlot), "setfield table"); err != nil {
			return fmt.Errorf("exit-resume-check: %s instr=%d %w", protoName, instrID, err)
		}
		return checkRel(int(ctx.TableValSlot), "setfield value")
	default:
		return fmt.Errorf("exit-resume-check: %s instr=%d unknown table op %d", protoName, instrID, ctx.TableOp)
	}
}

func checkSlotInRange(abs int, regs []runtime.Value, label string) error {
	if abs < 0 || abs >= len(regs) {
		return fmt.Errorf("%s abs slot %d outside regs len %d", label, abs, len(regs))
	}
	return nil
}

func protoNameForCheck(proto *vm.FuncProto) string {
	if proto == nil || proto.Name == "" {
		return "<unknown>"
	}
	return proto.Name
}
