package methodjit

import (
	"sort"

	"github.com/gscript/gscript/internal/vm"
)

// InstrCodeRange records the native-code byte range emitted for one IR
// instruction. Offsets are relative to the start of the compiled code block.
type InstrCodeRange struct {
	InstrID   int    `json:"ir_instr"`
	BlockID   int    `json:"block"`
	CodeStart int    `json:"code_start"`
	CodeEnd   int    `json:"code_end"`
	Pass      string `json:"pass"`
}

// IRASMMapEntry is one diagnostic source/bytecode/IR/native-code correlation
// row. CodeStart/CodeEnd are -1 for IR instructions that do not directly emit
// machine code, such as Phi/Nop after lowering.
type IRASMMapEntry struct {
	ProtoName  string `json:"proto"`
	Source     string `json:"source,omitempty"`
	SourceLine int    `json:"source_line,omitempty"`
	BytecodePC int    `json:"bytecode_pc"`
	BytecodeOp string `json:"bytecode_op,omitempty"`
	BlockID    int    `json:"block"`
	InstrID    int    `json:"ir_instr"`
	IROp       string `json:"ir_op"`
	IRType     string `json:"ir_type,omitempty"`
	CodeStart  int    `json:"code_start"`
	CodeEnd    int    `json:"code_end"`
	Pass       string `json:"pass,omitempty"`
}

func (i *Instr) setSourceFromPC(proto *vm.FuncProto, pc int) {
	if i == nil || proto == nil || pc < 0 || pc >= len(proto.Code) {
		return
	}
	i.HasSource = true
	i.SourcePC = pc
	if pc < len(proto.LineInfo) {
		i.SourceLine = proto.LineInfo[pc]
	}
}

func (i *Instr) copySourceFrom(src *Instr) {
	if i == nil || src == nil || !src.HasSource {
		return
	}
	i.HasSource = true
	i.SourcePC = src.SourcePC
	i.SourceLine = src.SourceLine
}

// BuildIRASMMap combines IR source metadata with emitter code ranges into a
// stable, JSON-friendly map. It includes every IR instruction in block order;
// instructions emitted more than once, such as normal and numeric self-entry
// bodies, appear once per emitted machine-code range.
func BuildIRASMMap(fn *Function, ranges []InstrCodeRange) []IRASMMapEntry {
	if fn == nil {
		return nil
	}
	byInstr := make(map[int][]InstrCodeRange)
	for _, r := range ranges {
		byInstr[r.InstrID] = append(byInstr[r.InstrID], r)
	}
	for id := range byInstr {
		sort.Slice(byInstr[id], func(i, j int) bool {
			return byInstr[id][i].CodeStart < byInstr[id][j].CodeStart
		})
	}

	var out []IRASMMapEntry
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			rs := byInstr[instr.ID]
			if len(rs) == 0 {
				out = append(out, buildIRASMMapEntry(fn, instr, InstrCodeRange{
					InstrID:   instr.ID,
					BlockID:   block.ID,
					CodeStart: -1,
					CodeEnd:   -1,
				}))
				continue
			}
			for _, r := range rs {
				out = append(out, buildIRASMMapEntry(fn, instr, r))
			}
		}
	}
	return out
}

func buildIRASMMapEntry(fn *Function, instr *Instr, r InstrCodeRange) IRASMMapEntry {
	entry := IRASMMapEntry{
		BlockID:    r.BlockID,
		InstrID:    instr.ID,
		IROp:       instr.Op.String(),
		IRType:     instr.Type.String(),
		BytecodePC: -1,
		CodeStart:  r.CodeStart,
		CodeEnd:    r.CodeEnd,
		Pass:       r.Pass,
	}
	if fn.Proto != nil {
		entry.ProtoName = fn.Proto.Name
		entry.Source = fn.Proto.Source
	}
	if instr.HasSource {
		entry.SourceLine = instr.SourceLine
		entry.BytecodePC = instr.SourcePC
		if fn.Proto != nil && instr.SourcePC >= 0 && instr.SourcePC < len(fn.Proto.Code) {
			entry.BytecodeOp = vm.OpName(vm.DecodeOp(fn.Proto.Code[instr.SourcePC]))
		}
	}
	if instr.Type == TypeUnknown {
		entry.IRType = ""
	}
	return entry
}
