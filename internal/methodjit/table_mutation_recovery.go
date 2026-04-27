package methodjit

import "fmt"

type tableMutationRecoveryClass uint8

const (
	tableMutationRecoverNone tableMutationRecoveryClass = iota
	tableMutationRecoverIdempotentOverwrite
	tableMutationRecoverReadBackedOverwrite
)

func (c tableMutationRecoveryClass) String() string {
	switch c {
	case tableMutationRecoverIdempotentOverwrite:
		return "idempotent-overwrite"
	case tableMutationRecoverReadBackedOverwrite:
		return "read-backed-overwrite"
	default:
		return "none"
	}
}

type tableMutationRecoverySite struct {
	InstrID       int
	BlockID       int
	Op            Op
	RecoveryClass tableMutationRecoveryClass
	Reason        string
}

type tableMutationRecoverySummary struct {
	Sites []tableMutationRecoverySite
}

func (s tableMutationRecoverySummary) firstUnadmitted() (tableMutationRecoverySite, bool) {
	for _, site := range s.Sites {
		if !tableMutationRecoveryClassAdmitted(site.RecoveryClass) {
			return site, true
		}
	}
	return tableMutationRecoverySite{}, false
}

func tableMutationRecoveryClassAdmitted(c tableMutationRecoveryClass) bool {
	// Only idempotent overwrites are admitted today. A read-backed overwrite
	// proves the key existed earlier on this path, but replaying it after a
	// restart can still change table contents (for example swaps), so it remains
	// diagnostic-only until the runtime has undo or all-guards-before-store
	// metadata.
	return c == tableMutationRecoverIdempotentOverwrite
}

type tableAccessKey struct {
	tableID int
	keyID   int
}

type tableReadWitness struct {
	valueID int
}

func analyzeLoopTableMutationRecovery(fn *Function) tableMutationRecoverySummary {
	if fn == nil {
		return tableMutationRecoverySummary{}
	}
	li := computeLoopInfo(fn)
	var summary tableMutationRecoverySummary

	for _, block := range fn.Blocks {
		if !li.loopBlocks[block.ID] {
			continue
		}
		witnesses := make(map[tableAccessKey]tableReadWitness)
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpGetTable:
				if key, ok := tableAccessKeyFor(instr); ok {
					witnesses[key] = tableReadWitness{valueID: instr.ID}
				}
			case OpSetTable:
				site := classifySetTableMutationRecovery(instr, witnesses)
				site.BlockID = block.ID
				summary.Sites = append(summary.Sites, site)
				if !tableMutationRecoveryClassAdmitted(site.RecoveryClass) && len(instr.Args) > 0 && instr.Args[0] != nil {
					clearTableWitnesses(witnesses, instr.Args[0].ID)
				}
			case OpSetField:
				summary.Sites = append(summary.Sites, tableMutationRecoverySite{
					InstrID:       instr.ID,
					BlockID:       block.ID,
					Op:            instr.Op,
					RecoveryClass: tableMutationRecoverNone,
					Reason:        "field write has no array overwrite recovery metadata",
				})
			case OpAppend:
				summary.Sites = append(summary.Sites, tableMutationRecoverySite{
					InstrID:       instr.ID,
					BlockID:       block.ID,
					Op:            instr.Op,
					RecoveryClass: tableMutationRecoverNone,
					Reason:        "append changes table length/shape",
				})
			case OpSetList:
				summary.Sites = append(summary.Sites, tableMutationRecoverySite{
					InstrID:       instr.ID,
					BlockID:       block.ID,
					Op:            instr.Op,
					RecoveryClass: tableMutationRecoverNone,
					Reason:        "bulk table write has no per-key recovery metadata",
				})
			}
		}
	}
	return summary
}

func classifySetTableMutationRecovery(instr *Instr, witnesses map[tableAccessKey]tableReadWitness) tableMutationRecoverySite {
	site := tableMutationRecoverySite{
		InstrID:       instr.ID,
		Op:            instr.Op,
		RecoveryClass: tableMutationRecoverNone,
		Reason:        "settable has no same-key read witness",
	}
	key, ok := tableAccessKeyFor(instr)
	if !ok {
		site.Reason = "settable arguments are incomplete"
		return site
	}
	witness, ok := witnesses[key]
	if !ok {
		return site
	}
	val := instr.Args[2]
	if val != nil && val.ID == witness.valueID {
		site.RecoveryClass = tableMutationRecoverIdempotentOverwrite
		site.Reason = "writes back the same value read from the same table/key"
		return site
	}
	site.RecoveryClass = tableMutationRecoverReadBackedOverwrite
	site.Reason = fmt.Sprintf("same-key read witness v%d exists, but stored value is v%d", witness.valueID, valueIDOrNegative(val))
	return site
}

func tableAccessKeyFor(instr *Instr) (tableAccessKey, bool) {
	if instr == nil || len(instr.Args) < 2 || instr.Args[0] == nil || instr.Args[1] == nil {
		return tableAccessKey{}, false
	}
	return tableAccessKey{tableID: instr.Args[0].ID, keyID: instr.Args[1].ID}, true
}

func clearTableWitnesses(witnesses map[tableAccessKey]tableReadWitness, tableID int) {
	for key := range witnesses {
		if key.tableID == tableID {
			delete(witnesses, key)
		}
	}
}

func valueIDOrNegative(v *Value) int {
	if v == nil {
		return -1
	}
	return v.ID
}
