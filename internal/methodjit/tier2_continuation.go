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
	Key       Tier2ContinuationKey
	InstrID   int
	Offset    int
	Ambiguous bool
}

func buildTier2Continuations(sites map[int]ExitSiteMeta, resumes []deferredResume, labelOffset func(string) int) map[Tier2ContinuationKey]Tier2Continuation {
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
			Key:     key,
			InstrID: dr.instrID,
			Offset:  off,
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func tier2ContinuationKindForExitOp(op string) Tier2ContinuationKind {
	switch op {
	case "Call", "Resume":
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
