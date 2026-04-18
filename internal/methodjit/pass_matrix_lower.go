// pass_matrix_lower.go — R45 Phase 2c lowering.
//
// Splits the compound DenseMatrix intrinsics into LICM-friendly
// primitives using row-pointer strength reduction (R46):
//
//   OpMatrixGetF(m, i, j)  →  flat    = OpMatrixFlat(m)
//                             stride  = OpMatrixStride(m)
//                             rowPtr  = OpMatrixRowPtr(flat, stride, i)
//                             result  = OpMatrixLoadFRow(rowPtr, j)
//
//   OpMatrixSetF(m, i, j, v) → flat   = OpMatrixFlat(m)
//                              stride = OpMatrixStride(m)
//                              rowPtr = OpMatrixRowPtr(flat, stride, i)
//                              OpMatrixStoreFRow(rowPtr, j, v)
//
// Flat/Stride depend only on m → LICM-hoistable when m is invariant.
// RowPtr depends on (flat, stride, i) → hoistable when i is also
// invariant (matmul's inner k-loop: a[i][k] has i invariant).
// LoadFRow depends on (rowPtr, j) → typically not hoistable (j varies).
//
// For matmul inner k-loop, post-LICM:
//   preheader (j-loop):  flat_a, stride_a, flat_b, stride_b
//   preheader (k-loop):  rowPtr_a = flat_a + i*stride_a*8
//   k body:              rowPtr_b = flat_b + k*stride_b*8
//                        load_a = LDR [rowPtr_a + k*8]
//                        load_b = LDR [rowPtr_b + j*8]
// rowPtr_a hoisted outside the k-loop; k body = 2×(ADD+MUL+LDR) + FMUL + FADD.
//
// Ordering in the pipeline: run AFTER typespec (so OpMatrixGetF's
// input types are finalized) and BEFORE LICM (so LICM sees the split).

package methodjit

// MatrixLowerPass rewrites OpMatrixGetF / OpMatrixSetF into the split
// form. Returns the modified function. Only walks existing
// instructions; new instructions are appended via splice.
func MatrixLowerPass(fn *Function) (*Function, error) {
	if fn == nil {
		return fn, nil
	}

	for _, block := range fn.Blocks {
		// Check if block has any OpMatrixGetF/OpMatrixSetF to lower.
		needsRewrite := false
		for _, instr := range block.Instrs {
			if instr.Op == OpMatrixGetF || instr.Op == OpMatrixSetF {
				needsRewrite = true
				break
			}
		}
		if !needsRewrite {
			continue
		}

		// Build a new instr list with the split form. We MUTATE the
		// original OpMatrixGetF/OpMatrixSetF into the Load/Store form
		// (keeping its SSA ID so downstream users don't break), and
		// INSERT the Flat/Stride ops before it.
		newInstrs := make([]*Instr, 0, len(block.Instrs)+2*len(block.Instrs))
		for _, instr := range block.Instrs {
			switch instr.Op {
			case OpMatrixGetF:
				if len(instr.Args) < 3 {
					newInstrs = append(newInstrs, instr)
					continue
				}
				m, i, j := instr.Args[0], instr.Args[1], instr.Args[2]
				flat := emitIRInstr(fn, block, OpMatrixFlat, TypeInt, []*Value{m}, 0, 0)
				stride := emitIRInstr(fn, block, OpMatrixStride, TypeInt, []*Value{m}, 0, 0)
				newInstrs = append(newInstrs, flat, stride)
				// R45 form (LoadFAt): the ARM64 pipeline absorbs MUL+ADD
				// inside LoadFAt's single-insn address computation.
				// R46's RowPtr split added an extra LSL+MOVreg that the
				// pipeline did NOT absorb, measuring 0.037 vs R45's
				// 0.035 median. Keep R45 form; RowPtr ops remain
				// available for future 3D-tensor work or hand-kernels.
				instr.Op = OpMatrixLoadFAt
				instr.Args = []*Value{flat.Value(), stride.Value(), i, j}
				newInstrs = append(newInstrs, instr)
			case OpMatrixSetF:
				if len(instr.Args) < 4 {
					newInstrs = append(newInstrs, instr)
					continue
				}
				m, i, j, v := instr.Args[0], instr.Args[1], instr.Args[2], instr.Args[3]
				flat := emitIRInstr(fn, block, OpMatrixFlat, TypeInt, []*Value{m}, 0, 0)
				stride := emitIRInstr(fn, block, OpMatrixStride, TypeInt, []*Value{m}, 0, 0)
				newInstrs = append(newInstrs, flat, stride)
				instr.Op = OpMatrixStoreFAt
				instr.Args = []*Value{flat.Value(), stride.Value(), i, j, v}
				newInstrs = append(newInstrs, instr)
			default:
				newInstrs = append(newInstrs, instr)
			}
		}
		block.Instrs = newInstrs
	}
	return fn, nil
}

// emitIRInstr allocates a new *Instr with a fresh SSA id, sets its Block,
// and returns it WITHOUT appending to any block's Instrs (caller does that).
func emitIRInstr(fn *Function, block *Block, op Op, typ Type, args []*Value, aux, aux2 int64) *Instr {
	instr := &Instr{
		ID:    fn.newValueID(),
		Op:    op,
		Type:  typ,
		Args:  args,
		Aux:   aux,
		Aux2:  aux2,
		Block: block,
	}
	return instr
}
