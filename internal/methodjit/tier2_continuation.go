//go:build darwin && arm64

package methodjit

// Tier2ContinuationKind groups exit-resume entries by the runtime protocol
// needed to materialize side effects before re-entering native code.
type Tier2ContinuationKind uint8

const (
	Tier2ContinuationOp Tier2ContinuationKind = iota
	Tier2ContinuationCall
	Tier2ContinuationGlobal
	Tier2ContinuationTable
)

// Tier2ContinuationKey is intentionally independent of IR instruction IDs,
// which can change across feedback-specialized recompiles.
type Tier2ContinuationKey struct {
	PC          int
	Kind        Tier2ContinuationKind
	NumericPass bool
}

// Tier2Continuation records one emitted resume entry for a stable source key.
// Ambiguous means multiple IR exits in this version share the same stable key;
// such entries are useful for diagnostics but not safe for version switching.
type Tier2Continuation struct {
	Key              Tier2ContinuationKey
	InstrID          int
	Offset           int
	Ambiguous        bool
	LiveSlots        []exitResumeLiveSlot
	Switchable       bool
	NotSwitchableWhy string
}

func buildTier2Continuations(sites map[int]ExitSiteMeta, resumes []deferredResume, live exitResumeLiveMetadata, numRegs int, labelOffset func(string) int) map[Tier2ContinuationKey]Tier2Continuation {
	if len(sites) == 0 || len(resumes) == 0 || labelOffset == nil {
		return nil
	}
	out := make(map[Tier2ContinuationKey]Tier2Continuation)
	for _, dr := range resumes {
		meta, ok := sites[dr.instrID]
		if !ok || meta.PC < 0 {
			continue
		}
		off := labelOffset(callExitResumeLabelForPass(dr.instrID, dr.numericPass))
		if off < 0 {
			continue
		}
		key := Tier2ContinuationKey{
			PC:          meta.PC,
			Kind:        tier2ContinuationKindForExitOp(meta.Op),
			NumericPass: dr.numericPass,
		}
		cont, exists := out[key]
		if exists {
			if cont.InstrID != dr.instrID || cont.Offset != off {
				cont.Ambiguous = true
				out[key] = cont
			}
			continue
		}
		out[key] = Tier2Continuation{
			Key:       key,
			InstrID:   dr.instrID,
			Offset:    off,
			LiveSlots: live.slots(dr.instrID, dr.numericPass),
		}
		cont = out[key]
		cont.Switchable, cont.NotSwitchableWhy = classifyTier2ContinuationSwitchability(cont, numRegs)
		out[key] = cont
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func tier2ContinuationKindForExitOp(op string) Tier2ContinuationKind {
	switch op {
	case "Call", "CallFloor", "FieldCallFloor", "Resume":
		return Tier2ContinuationCall
	case "GetGlobal", "SetGlobal":
		return Tier2ContinuationGlobal
	case "NewTable", "NewFixedTable",
		"GetTable", "SetTable", "GetField", "GetFieldNumToFloat", "SetField",
		"TableArrayLoad", "TableArrayStore", "TableArraySwap", "TableBoolArrayFill",
		"SetList", "Append":
		return Tier2ContinuationTable
	default:
		return Tier2ContinuationOp
	}
}

func classifyTier2ContinuationSwitchability(cont Tier2Continuation, numRegs int) (bool, string) {
	if cont.Ambiguous {
		return false, "ambiguous_key"
	}
	if numRegs <= 0 {
		return false, "unknown_vm_register_count"
	}
	for _, live := range cont.LiveSlots {
		if live.Slot < 0 {
			return false, "negative_slot"
		}
		if live.Slot >= numRegs {
			return false, "version_local_temp_slot"
		}
	}
	return true, ""
}

func tier2ContinuationStats(cf *CompiledFunction) (total, ambiguous int) {
	if cf == nil {
		return 0, 0
	}
	for _, cont := range cf.Continuations {
		total++
		if cont.Ambiguous {
			ambiguous++
		}
	}
	return total, ambiguous
}

func tier2ContinuationSwitchabilityStats(cf *CompiledFunction) (switchable int, reasons map[string]int) {
	if cf == nil {
		return 0, nil
	}
	for _, cont := range cf.Continuations {
		if cont.Switchable {
			switchable++
			continue
		}
		reason := cont.NotSwitchableWhy
		if reason == "" {
			reason = "unknown"
		}
		if reasons == nil {
			reasons = make(map[string]int)
		}
		reasons[reason]++
	}
	return switchable, reasons
}

func tier2ContinuationSwitchabilityForExit(cf *CompiledFunction, exitName string, pc int) (known bool, switchable bool, reason string) {
	if cf == nil || pc < 0 {
		return false, false, ""
	}
	kind, ok := tier2ContinuationKindForExitName(exitName)
	if !ok {
		return false, false, ""
	}
	for _, numeric := range []bool{false, true} {
		cont, ok := cf.Continuations[Tier2ContinuationKey{PC: pc, Kind: kind, NumericPass: numeric}]
		if !ok {
			continue
		}
		if cont.Switchable {
			return true, true, ""
		}
		if cont.NotSwitchableWhy != "" {
			reason = cont.NotSwitchableWhy
		}
		known = true
	}
	if known {
		if reason == "" {
			reason = "unknown"
		}
		return true, false, reason
	}
	return false, false, ""
}

func tier2ContinuationKindForExitName(exitName string) (Tier2ContinuationKind, bool) {
	switch exitName {
	case "ExitCallExit", "ExitNativeCallExit":
		return Tier2ContinuationCall, true
	case "ExitGlobalExit":
		return Tier2ContinuationGlobal, true
	case "ExitTableExit":
		return Tier2ContinuationTable, true
	case "ExitOpExit":
		return Tier2ContinuationOp, true
	default:
		return 0, false
	}
}

func tier2ContinuationKeyForRuntimeExit(cf *CompiledFunction, ctx *ExecContext) (Tier2ContinuationKey, bool) {
	if cf == nil || ctx == nil {
		return Tier2ContinuationKey{}, false
	}
	instrID := 0
	var kind Tier2ContinuationKind
	switch ctx.ExitCode {
	case ExitCallExit, ExitNativeCallExit:
		instrID = int(ctx.CallID)
		kind = Tier2ContinuationCall
	case ExitGlobalExit:
		instrID = int(ctx.GlobalExitID)
		kind = Tier2ContinuationGlobal
	case ExitTableExit:
		instrID = int(ctx.TableExitID)
		kind = Tier2ContinuationTable
	case ExitOpExit:
		instrID = int(ctx.OpExitID)
		kind = Tier2ContinuationOp
	default:
		return Tier2ContinuationKey{}, false
	}
	meta, ok := cf.ExitSites[instrID]
	if !ok || meta.PC < 0 {
		return Tier2ContinuationKey{}, false
	}
	return Tier2ContinuationKey{PC: meta.PC, Kind: kind, NumericPass: ctx.ResumeNumericPass != 0}, true
}

func tier2ContinuationExactStateCompatible(oldCont, newCont Tier2Continuation) (bool, string) {
	if oldCont.Ambiguous || newCont.Ambiguous {
		return false, "ambiguous_key"
	}
	if len(oldCont.LiveSlots) != len(newCont.LiveSlots) {
		return false, "live_slot_count_changed"
	}
	oldBySlot := make(map[int]exitResumeLiveSlot, len(oldCont.LiveSlots))
	for _, live := range oldCont.LiveSlots {
		oldBySlot[live.Slot] = live
	}
	for _, live := range newCont.LiveSlots {
		old, ok := oldBySlot[live.Slot]
		if !ok {
			return false, "live_slot_mapping_changed"
		}
		if old.Repr != live.Repr {
			return false, "live_slot_repr_changed"
		}
	}
	return true, ""
}
