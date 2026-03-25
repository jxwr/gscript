package jit

// ──────────────────────────────────────────────────────────────────────────────
// Memory Operations
// ──────────────────────────────────────────────────────────────────────────────

// LDR: Xt = [Xn + #offset] (64-bit load, unsigned offset, must be 8-byte aligned)
func (a *Assembler) LDR(rt, rn Reg, offset int) {
	// LDR Xt, [Xn, #pimm]: 1|1|11|1|00|01|0|imm12|Rn|Rt
	// pimm = offset / 8
	pimm := offset >> 3
	a.emit(0xF9400000 | uint32(pimm&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// STR: [Xn + #offset] = Xt (64-bit store, unsigned offset, must be 8-byte aligned)
func (a *Assembler) STR(rt, rn Reg, offset int) {
	// STR Xt, [Xn, #pimm]: 1|1|11|1|00|00|0|imm12|Rn|Rt
	pimm := offset >> 3
	a.emit(0xF9000000 | uint32(pimm&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// STRpre: [Xn + #simm9]! = Xt (pre-index store, updates Xn)
func (a *Assembler) STRpre(rt, rn Reg, simm9 int) {
	// STR Xt, [Xn, #simm9]!: 1|1|11|1|00|00|0|imm9|11|Rn|Rt
	imm9 := uint32(simm9) & 0x1FF
	a.emit(0xF8000C00 | imm9<<12 | uint32(rn)<<5 | uint32(rt))
}

// STRreg: [Xn + Xm, LSL #3] = Xt (register offset 64-bit store, shifted by 3)
func (a *Assembler) STRreg(rt, rn, rm Reg) {
	// STR Xt, [Xn, Xm, LSL #3]: 11|11|1|00|00|1|Rm|011|1|10|Rn|Rt
	// Bit 12 (S) = 1 enables LSL #3 scaling for 64-bit access.
	a.emit(0xF8207800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rt))
}

// LDRpost: Xt = [Xn], #simm9 (post-index load, updates Xn)
func (a *Assembler) LDRpost(rt, rn Reg, simm9 int) {
	// LDR Xt, [Xn], #simm9: 1|1|11|1|00|01|0|imm9|01|Rn|Rt
	imm9 := uint32(simm9) & 0x1FF
	a.emit(0xF8400400 | imm9<<12 | uint32(rn)<<5 | uint32(rt))
}

// LDRreg: Xt = [Xn + Xm, LSL #3] (register offset load, 64-bit, scaled)
func (a *Assembler) LDRreg(rt, rn, rm Reg) {
	// LDR Xt, [Xn, Xm, LSL #3]: 11|11|1|00|01|1|Rm|011|1|10|Rn|Rt
	// Bit 12 (S) = 1 enables LSL #3 scaling for 64-bit access.
	a.emit(0xF8607800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rt))
}

// LDRBreg: Wt = [Xn + Xm] (register offset byte load)
func (a *Assembler) LDRBreg(rt, rn, rm Reg) {
	// LDRB Wt, [Xn, Xm]: 00|11|1|00|01|1|Rm|011|0|10|Rn|Rt
	a.emit(0x38606800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rt))
}

// LDRB: Wt = [Xn + #offset] (byte load, zero extend)
func (a *Assembler) LDRB(rt, rn Reg, offset int) {
	// LDRB Wt, [Xn, #pimm]: 00|11|1|00|01|0|imm12|Rn|Rt
	a.emit(0x39400000 | uint32(offset&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// STRB: [Xn + #offset] = Wt (byte store)
func (a *Assembler) STRB(rt, rn Reg, offset int) {
	// STRB Wt, [Xn, #pimm]: 00|11|1|00|00|0|imm12|Rn|Rt
	a.emit(0x39000000 | uint32(offset&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// STRBreg: [Xn + Xm] = Wt (register offset byte store)
func (a *Assembler) STRBreg(rt, rn, rm Reg) {
	// STRB Wt, [Xn, Xm]: 00|11|1|00|00|1|Rm|011|0|10|Rn|Rt
	a.emit(0x38206800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rt))
}

// LDRW: Wt = [Xn + #offset] (32-bit load, unsigned offset, 4-byte aligned)
func (a *Assembler) LDRW(rt, rn Reg, offset int) {
	// LDR Wt, [Xn, #pimm]: 1|0|11|1|00|01|0|imm12|Rn|Rt
	pimm := offset >> 2
	a.emit(0xB9400000 | uint32(pimm&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// LDP: Load pair of 64-bit registers. offset must be 8-byte aligned, range [-512, 504].
func (a *Assembler) LDP(rt1, rt2, rn Reg, offset int) {
	// LDP Xt1, Xt2, [Xn, #simm7*8]: 1|0|10|1|0|01|1|simm7|Rt2|Rn|Rt1
	simm7 := offset >> 3
	a.emit(0xA9400000 | uint32(simm7&0x7F)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt1))
}

// STP: Store pair of 64-bit registers. offset must be 8-byte aligned, range [-512, 504].
func (a *Assembler) STP(rt1, rt2, rn Reg, offset int) {
	// STP Xt1, Xt2, [Xn, #simm7*8]: 1|0|10|1|0|00|1|simm7|Rt2|Rn|Rt1
	simm7 := offset >> 3
	a.emit(0xA9000000 | uint32(simm7&0x7F)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt1))
}

// STP pre-index: STP Xt1, Xt2, [Xn, #simm7]! (pre-indexed store pair)
func (a *Assembler) STPpre(rt1, rt2, rn Reg, offset int) {
	// 1|0|10|1|0|01|1|simm7|Rt2|Rn|Rt1 (pre-index = opc bit pattern 101)
	simm7 := offset >> 3
	a.emit(0xA9800000 | uint32(simm7&0x7F)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt1))
}

// SIMD pair store: STP Dt1, Dt2, [Xn, #imm] (for saving callee-saved D8-D15)
func (a *Assembler) FSTP(rt1, rt2 FReg, rn Reg, offset int) {
	simm7 := offset >> 3
	a.emit(0x6D000000 | uint32(simm7&0x7F)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt1))
}

// SIMD pair load: LDP Dt1, Dt2, [Xn, #imm] (for restoring callee-saved D8-D15)
func (a *Assembler) FLDP(rt1, rt2 FReg, rn Reg, offset int) {
	simm7 := offset >> 3
	a.emit(0x6D400000 | uint32(simm7&0x7F)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt1))
}

// LDP post-index: LDP Xt1, Xt2, [Xn], #simm7 (post-indexed load pair)
func (a *Assembler) LDPpost(rt1, rt2, rn Reg, offset int) {
	// 1|0|10|1|0|00|1|simm7|Rt2|Rn|Rt1 (post-index = opc bit pattern 100... wait)
	// Actually: LDP post-index: 10 101 0 001 1 simm7 Rt2 Rn Rt1
	simm7 := offset >> 3
	a.emit(0xA8C00000 | uint32(simm7&0x7F)<<15 | uint32(rt2)<<10 | uint32(rn)<<5 | uint32(rt1))
}
