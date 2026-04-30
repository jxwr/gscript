// printer.go provides human-readable IR output for debugging.
// Print(fn) returns a string showing all basic blocks, instructions,
// phi nodes, and control flow edges in the function's CFG SSA IR.

package methodjit

import (
	"fmt"
	"math"
	"strings"
)

// Print returns a human-readable representation of the function's CFG SSA IR.
func Print(fn *Function) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "function %s (%d blocks, %d regs)\n",
		fn.Proto.Name, len(fn.Blocks), fn.NumRegs)

	for _, b := range fn.Blocks {
		printBlock(&sb, b, b == fn.Entry)
	}
	return sb.String()
}

func printBlock(sb *strings.Builder, b *Block, isEntry bool) {
	label := fmt.Sprintf("B%d", b.ID)
	if isEntry {
		label += " (entry)"
	}

	// Predecessors
	if len(b.Preds) > 0 {
		preds := make([]string, len(b.Preds))
		for i, p := range b.Preds {
			preds[i] = fmt.Sprintf("B%d", p.ID)
		}
		fmt.Fprintf(sb, "%s: ; preds: %s\n", label, strings.Join(preds, ", "))
	} else {
		fmt.Fprintf(sb, "%s:\n", label)
	}

	// Instructions
	for _, instr := range b.Instrs {
		printInstr(sb, instr)
	}
	sb.WriteString("\n")
}

func printInstr(sb *strings.Builder, i *Instr) {
	// Result
	if i.Op.IsTerminator() {
		fmt.Fprintf(sb, "    ")
	} else {
		fmt.Fprintf(sb, "    v%-3d = ", i.ID)
	}

	// Op name
	fmt.Fprintf(sb, "%-18s", i.Op.String())

	// Arguments
	switch i.Op {
	case OpConstInt:
		fmt.Fprintf(sb, "%d", i.Aux)
	case OpConstFloat:
		fmt.Fprintf(sb, "%.6g", math.Float64frombits(uint64(i.Aux)))
	case OpConstBool:
		if i.Aux != 0 {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case OpConstNil:
		sb.WriteString("nil")
	case OpLoadSlot:
		fmt.Fprintf(sb, "slot[%d]", i.Aux)
	case OpStoreSlot:
		fmt.Fprintf(sb, "slot[%d] = v%d", i.Aux, i.Args[0].ID)
	case OpJump:
		if len(i.Block.Succs) > 0 {
			fmt.Fprintf(sb, "→ B%d", i.Block.Succs[0].ID)
		}
	case OpBranch:
		if len(i.Args) > 0 && len(i.Block.Succs) >= 2 {
			fmt.Fprintf(sb, "v%d → B%d, B%d",
				i.Args[0].ID, i.Block.Succs[0].ID, i.Block.Succs[1].ID)
		}
	case OpReturn:
		args := make([]string, len(i.Args))
		for j, a := range i.Args {
			args[j] = fmt.Sprintf("v%d", a.ID)
		}
		sb.WriteString(strings.Join(args, ", "))
	case OpCall:
		args := make([]string, len(i.Args))
		for j, a := range i.Args {
			args[j] = fmt.Sprintf("v%d", a.ID)
		}
		sb.WriteString(strings.Join(args, ", "))
	case OpGetGlobal, OpSetGlobal:
		fmt.Fprintf(sb, "globals[%d]", i.Aux)
		if len(i.Args) > 0 {
			fmt.Fprintf(sb, " = v%d", i.Args[0].ID)
		}
	case OpGetField, OpGetFieldNumToFloat, OpSetField:
		if len(i.Args) > 0 {
			fmt.Fprintf(sb, "v%d.field[%d]", i.Args[0].ID, i.Aux)
		}
		if i.Op == OpSetField && len(i.Args) > 1 {
			fmt.Fprintf(sb, " = v%d", i.Args[1].ID)
		}
	case OpNewFixedTable:
		args := make([]string, len(i.Args))
		for j, a := range i.Args {
			args[j] = fmt.Sprintf("v%d", a.ID)
		}
		fmt.Fprintf(sb, "ctor[%d]/%d(%s)", i.Aux, i.Aux2, strings.Join(args, ", "))
	case OpTableBoolArrayFill:
		if len(i.Args) >= 3 {
			val := "false"
			if i.Aux == 2 {
				val = "true"
			}
			fmt.Fprintf(sb, "v%d, v%d..v%d = %s", i.Args[0].ID, i.Args[1].ID, i.Args[2].ID, val)
		}
	case OpGuardType:
		if len(i.Args) > 0 {
			fmt.Fprintf(sb, "v%d is %s", i.Args[0].ID, Type(i.Aux).String())
		}
	case OpGuardIntRange:
		if len(i.Args) > 0 {
			fmt.Fprintf(sb, "v%d in [%d,%d]", i.Args[0].ID, i.Aux, i.Aux2)
		}
	case OpPhi:
		args := make([]string, len(i.Args))
		for j, a := range i.Args {
			if j < len(i.Block.Preds) {
				args[j] = fmt.Sprintf("B%d:v%d", i.Block.Preds[j].ID, a.ID)
			} else {
				args[j] = fmt.Sprintf("v%d", a.ID)
			}
		}
		sb.WriteString(strings.Join(args, ", "))
	default:
		// Generic: print all args
		args := make([]string, len(i.Args))
		for j, a := range i.Args {
			args[j] = fmt.Sprintf("v%d", a.ID)
		}
		sb.WriteString(strings.Join(args, ", "))
	}

	// Type annotation
	if i.Type != TypeUnknown && !i.Op.IsTerminator() {
		fmt.Fprintf(sb, " : %s", i.Type.String())
	}

	sb.WriteString("\n")
}
