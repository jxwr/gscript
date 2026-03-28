package jit

// ──────────────────────────────────────────────────────────────────────────────
// Floating Point Instructions (double precision)
// ──────────────────────────────────────────────────────────────────────────────

// FADDd: Dd = Dn + Dm (double)
func (a *Assembler) FADDd(rd, rn, rm FReg) {
	// 0|00|11110|01|1|Rm|001|0|10|Rn|Rd
	a.emit(0x1E602800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// FSUBd: Dd = Dn - Dm (double)
func (a *Assembler) FSUBd(rd, rn, rm FReg) {
	// 0|00|11110|01|1|Rm|001|1|10|Rn|Rd
	a.emit(0x1E603800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// FMULd: Dd = Dn * Dm (double)
func (a *Assembler) FMULd(rd, rn, rm FReg) {
	// 0|00|11110|01|1|Rm|000|0|10|Rn|Rd
	a.emit(0x1E600800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// FDIVd: Dd = Dn / Dm (double)
func (a *Assembler) FDIVd(rd, rn, rm FReg) {
	// 0|00|11110|01|1|Rm|000|1|10|Rn|Rd
	a.emit(0x1E601800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// FCMPd: compare Dn, Dm (sets NZCV flags)
func (a *Assembler) FCMPd(rn, rm FReg) {
	// 0|00|11110|01|1|Rm|00|1000|Rn|0|0000
	a.emit(0x1E602000 | uint32(rm)<<16 | uint32(rn)<<5)
}

// FMADDd: Dd = Da + Dn * Dm (fused multiply-add, double)
// Single instruction, higher precision than separate MUL+ADD.
func (a *Assembler) FMADDd(rd, rn, rm, ra FReg) {
	// 0|00|11111|01|0|Rm|0|Ra|Rn|Rd
	a.emit(0x1F400000 | uint32(rm)<<16 | uint32(ra)<<10 | uint32(rn)<<5 | uint32(rd))
}

// FMSUBd: Dd = Da - Dn * Dm (fused multiply-subtract, double)
func (a *Assembler) FMSUBd(rd, rn, rm, ra FReg) {
	// 0|00|11111|01|0|Rm|1|Ra|Rn|Rd
	a.emit(0x1F408000 | uint32(rm)<<16 | uint32(ra)<<10 | uint32(rn)<<5 | uint32(rd))
}

// FSQRTd: Dd = sqrt(Dn) (double)
func (a *Assembler) FSQRTd(rd, rn FReg) {
	// 0|00|11110|01|1|00001|11000|Rn|Rd
	a.emit(0x1E61C000 | uint32(rn)<<5 | uint32(rd))
}

// SCVTF: Dd = (double)Xn (signed int64 to float64)
func (a *Assembler) SCVTF(rd FReg, rn Reg) {
	// 1|00|11110|01|1|00010|000000|Rn|Rd
	a.emit(0x9E620000 | uint32(rn)<<5 | uint32(rd))
}

// FCVTZS: Xd = (int64)Dn (float64 to signed int64, round toward zero)
func (a *Assembler) FCVTZS(rd Reg, rn FReg) {
	// 1|00|11110|01|1|11000|000000|Rn|Rd
	a.emit(0x9E780000 | uint32(rn)<<5 | uint32(rd))
}

// FMOVd: Dd = Dn (register to register copy, double precision)
func (a *Assembler) FMOVd(rd, rn FReg) {
	// FMOV Dd, Dn: 0|00|11110|01|1|00000|010000|Rn|Rd
	a.emit(0x1E604000 | uint32(rn)<<5 | uint32(rd))
}

// FMOVtoFP: Dd = Xn (move bits, no conversion)
func (a *Assembler) FMOVtoFP(rd FReg, rn Reg) {
	// 1|00|11110|01|1|00111|000000|Rn|Rd
	a.emit(0x9E670000 | uint32(rn)<<5 | uint32(rd))
}

// FMOVtoGP: Xd = Dn (move bits, no conversion)
func (a *Assembler) FMOVtoGP(rd Reg, rn FReg) {
	// 1|00|11110|01|1|00110|000000|Rn|Rd
	a.emit(0x9E660000 | uint32(rn)<<5 | uint32(rd))
}

// FLDRd: Dt = [Xn + #offset] (64-bit FP load, offset must be 8-byte aligned)
func (a *Assembler) FLDRd(rt FReg, rn Reg, offset int) {
	// LDR Dt, [Xn, #pimm]: 1|1|11|1|10|01|0|imm12|Rn|Rt
	pimm := offset >> 3
	a.emit(0xFD400000 | uint32(pimm&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// FSTRd: [Xn + #offset] = Dt (64-bit FP store, offset must be 8-byte aligned)
func (a *Assembler) FSTRd(rt FReg, rn Reg, offset int) {
	// STR Dt, [Xn, #pimm]: 1|1|11|1|10|00|0|imm12|Rn|Rt
	pimm := offset >> 3
	a.emit(0xFD000000 | uint32(pimm&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// FABSd: Dd = |Dn| (absolute value, double)
func (a *Assembler) FABSd(dst, src FReg)    { a.emit(0x1e60c000 | uint32(src)<<5 | uint32(dst)) }

// FRINTMd: Dd = floor(Dn) (round toward -inf, double)
func (a *Assembler) FRINTMd(dst, src FReg)  { a.emit(0x1e654000 | uint32(src)<<5 | uint32(dst)) }

// FRINTPd: Dd = ceil(Dn) (round toward +inf, double)
func (a *Assembler) FRINTPd(dst, src FReg)  { a.emit(0x1e64c000 | uint32(src)<<5 | uint32(dst)) }

// FMAXNMd: Dd = maxnum(Dn, Dm) (IEEE 754 maxNum, double)
func (a *Assembler) FMAXNMd(dst, src1, src2 FReg) { a.emit(0x1e626800 | uint32(src2)<<16 | uint32(src1)<<5 | uint32(dst)) }

// FMINNMd: Dd = minnum(Dn, Dm) (IEEE 754 minNum, double)
func (a *Assembler) FMINNMd(dst, src1, src2 FReg) { a.emit(0x1e627800 | uint32(src2)<<16 | uint32(src1)<<5 | uint32(dst)) }
