package jit

// ──────────────────────────────────────────────────────────────────────────────
// Integer Data Processing
// ──────────────────────────────────────────────────────────────────────────────

// ADDreg: Xd = Xn + Xm (64-bit)
func (a *Assembler) ADDreg(rd, rn, rm Reg) {
	// 1|00|01011|00|0|Rm|000000|Rn|Rd
	a.emit(0x8B000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// ADDregLSL: Xd = Xn + (Xm << shift) (64-bit, LSL shift 0-63)
func (a *Assembler) ADDregLSL(rd, rn, rm Reg, shift uint8) {
	// 1|00|01011|00|0|Rm|imm6|Rn|Rd  (shift_type=00=LSL)
	a.emit(0x8B000000 | uint32(rm)<<16 | uint32(shift&0x3F)<<10 | uint32(rn)<<5 | uint32(rd))
}

// ADDimm: Xd = Xn + #imm12 (64-bit, no shift)
func (a *Assembler) ADDimm(rd, rn Reg, imm12 uint16) {
	// 1|00|100010|0|imm12|Rn|Rd
	a.emit(0x91000000 | uint32(imm12&0xFFF)<<10 | uint32(rn)<<5 | uint32(rd))
}

// SUBreg: Xd = Xn - Xm (64-bit)
func (a *Assembler) SUBreg(rd, rn, rm Reg) {
	// 1|10|01011|00|0|Rm|000000|Rn|Rd
	a.emit(0xCB000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// SUBimm: Xd = Xn - #imm12 (64-bit, no shift)
func (a *Assembler) SUBimm(rd, rn Reg, imm12 uint16) {
	// 1|10|100010|0|imm12|Rn|Rd
	a.emit(0xD1000000 | uint32(imm12&0xFFF)<<10 | uint32(rn)<<5 | uint32(rd))
}

// MUL: Xd = Xn * Xm (64-bit)
func (a *Assembler) MUL(rd, rn, rm Reg) {
	// MADD Xd, Xn, Xm, XZR  = 1|00|11011|000|Rm|0|Ra=11111|Rn|Rd
	a.emit(0x9B007C00 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// MADD: Xd = Xn*Xm + Xa (64-bit)
func (a *Assembler) MADD(rd, rn, rm, ra Reg) {
	// 1|00|11011|000|Rm|0|Ra|Rn|Rd
	a.emit(0x9B000000 | uint32(rm)<<16 | uint32(ra)<<10 | uint32(rn)<<5 | uint32(rd))
}

// SDIV: Xd = Xn / Xm (signed 64-bit)
func (a *Assembler) SDIV(rd, rn, rm Reg) {
	// 1|00|11010110|Rm|00001|1|Rn|Rd
	a.emit(0x9AC00C00 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// MSUB: Xd = Xa - Xn*Xm (64-bit). Used for modulo: a%b = a - (a/b)*b.
func (a *Assembler) MSUB(rd, rn, rm, ra Reg) {
	// 1|00|11011|000|Rm|1|Ra|Rn|Rd
	a.emit(0x9B008000 | uint32(rm)<<16 | uint32(ra)<<10 | uint32(rn)<<5 | uint32(rd))
}

// NEG: Xd = -Xm (SUB Xd, XZR, Xm)
func (a *Assembler) NEG(rd, rm Reg) {
	a.SUBreg(rd, XZR, rm)
}

// MOVreg: Xd = Xn (ORR Xd, XZR, Xn)
func (a *Assembler) MOVreg(rd, rn Reg) {
	// ORR Xd, XZR, Xn
	// 1|01|01010|00|0|Rm|000000|Rn=11111|Rd  (but MOV is ORR with Rn=XZR)
	a.emit(0xAA0003E0 | uint32(rn)<<16 | uint32(rd))
}

// MOVimm16: MOVZ Xd, #imm16 (move 16-bit immediate, zero others)
func (a *Assembler) MOVimm16(rd Reg, imm16 uint16) {
	// MOVZ: 1|10|100101|hw=00|imm16|Rd
	a.emit(0xD2800000 | uint32(imm16)<<5 | uint32(rd))
}

// MOVKimm16: MOVK Xd, #imm16, LSL #(hw*16) (move 16-bit, keep other bits)
func (a *Assembler) MOVKimm16(rd Reg, imm16 uint16, hw uint8) {
	// MOVK: 1|11|100101|hw|imm16|Rd
	a.emit(0xF2800000 | uint32(hw&3)<<21 | uint32(imm16)<<5 | uint32(rd))
}

// MOVNimm16: MOVN Xd, #imm16 (move NOT of 16-bit immediate)
func (a *Assembler) MOVNimm16(rd Reg, imm16 uint16) {
	// MOVN: 1|00|100101|hw=00|imm16|Rd
	a.emit(0x92800000 | uint32(imm16)<<5 | uint32(rd))
}

// LoadImm64 loads a full 64-bit immediate into rd using MOVZ/MOVK sequence.
func (a *Assembler) LoadImm64(rd Reg, val int64) {
	uval := uint64(val)

	// Check if the value fits in a single MOVZ
	if uval <= 0xFFFF {
		a.MOVimm16(rd, uint16(uval))
		return
	}

	// Check if it's a small negative (fits in MOVN)
	if val < 0 && val >= -0x10000 {
		a.MOVNimm16(rd, uint16(^val))
		return
	}

	// General case: MOVZ + up to 3 MOVKs
	first := true
	for hw := uint8(0); hw < 4; hw++ {
		chunk := uint16((uval >> (hw * 16)) & 0xFFFF)
		if chunk == 0 && first {
			continue // skip zero chunks before the first non-zero
		}
		if first {
			// MOVZ with hw
			a.emit(0xD2800000 | uint32(hw)<<21 | uint32(chunk)<<5 | uint32(rd))
			first = true
			first = false
		} else {
			a.MOVKimm16(rd, chunk, hw)
		}
	}

	// If all chunks were zero (val == 0), emit MOVZ Xd, #0
	if first {
		a.MOVimm16(rd, 0)
	}
}

// CMPreg: CMP Xn, Xm (SUBS XZR, Xn, Xm)
func (a *Assembler) CMPreg(rn, rm Reg) {
	// SUBS XZR, Xn, Xm: 1|11|01011|00|0|Rm|000000|Rn|Rd=11111
	a.emit(0xEB00001F | uint32(rm)<<16 | uint32(rn)<<5)
}

// CMPimm: CMP Xn, #imm12 (SUBS XZR, Xn, #imm12)
func (a *Assembler) CMPimm(rn Reg, imm12 uint16) {
	// SUBS XZR, Xn, #imm12: 1|11|100010|0|imm12|Rn|Rd=11111
	a.emit(0xF100001F | uint32(imm12&0xFFF)<<10 | uint32(rn)<<5)
}

// ANDreg: Xd = Xn & Xm (64-bit)
func (a *Assembler) ANDreg(rd, rn, rm Reg) {
	// 1|00|01010|00|0|Rm|000000|Rn|Rd
	a.emit(0x8A000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// ORRreg: Xd = Xn | Xm (64-bit)
func (a *Assembler) ORRreg(rd, rn, rm Reg) {
	// 1|01|01010|00|0|Rm|000000|Rn|Rd
	a.emit(0xAA000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// EORreg: Xd = Xn ^ Xm (64-bit)
func (a *Assembler) EORreg(rd, rn, rm Reg) {
	// 1|10|01010|00|0|Rm|000000|Rn|Rd
	a.emit(0xCA000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// ──────────────────────────────────────────────────────────────────────────────
// 32-bit variants (W-form) for byte/word operations
// ──────────────────────────────────────────────────────────────────────────────

// MOVimm16W: MOVZ Wd, #imm16 (32-bit move)
func (a *Assembler) MOVimm16W(rd Reg, imm16 uint16) {
	// MOVZ 32-bit: 0|10|100101|hw=00|imm16|Rd
	a.emit(0x52800000 | uint32(imm16)<<5 | uint32(rd))
}

// CMPimmW: CMP Wn, #imm12 (32-bit, SUBS WZR, Wn, #imm12)
func (a *Assembler) CMPimmW(rn Reg, imm12 uint16) {
	// 0|11|100010|0|imm12|Rn|Rd=11111
	a.emit(0x7100001F | uint32(imm12&0xFFF)<<10 | uint32(rn)<<5)
}

// LSLimm: Rd = Rn << shift (alias of UBFM)
func (a *Assembler) LSLimm(rd, rn Reg, shift uint8) {
	// LSL Xd, Xn, #shift = UBFM Xd, Xn, #(64-shift), #(63-shift)
	immr := uint32(64-shift) & 63
	imms := uint32(63-shift) & 63
	a.emit(0xD3400000 | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd))
}

// LSRimm: Rd = Rn >> shift (logical shift right, alias of UBFM)
func (a *Assembler) LSRimm(rd, rn Reg, shift uint8) {
	// LSR Xd, Xn, #shift = UBFM Xd, Xn, #shift, #63
	immr := uint32(shift) & 63
	imms := uint32(63)
	a.emit(0xD3400000 | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd))
}

// SBFX: Signed Bitfield Extract. Xd = SignExtend(Xn[lsb+width-1:lsb])
// Alias of SBFM Xd, Xn, #lsb, #(lsb+width-1)
func (a *Assembler) SBFX(rd, rn Reg, lsb, width uint8) {
	// SBFM: 1|00|100110|1|immr|imms|Rn|Rd
	immr := uint32(lsb) & 63
	imms := uint32(lsb+width-1) & 63
	a.emit(0x93400000 | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd))
}

// UBFX: Unsigned Bitfield Extract. Xd = ZeroExtend(Xn[lsb+width-1:lsb])
// Alias of UBFM Xd, Xn, #lsb, #(lsb+width-1)
func (a *Assembler) UBFX(rd, rn Reg, lsb, width uint8) {
	// UBFM: 1|10|100110|1|immr|imms|Rn|Rd
	immr := uint32(lsb) & 63
	imms := uint32(lsb+width-1) & 63
	a.emit(0xD3400000 | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd))
}

// ──────────────────────────────────────────────────────────────────────────────
// Conditional select
// ──────────────────────────────────────────────────────────────────────────────

// CSINC: Xd = (cond) ? Xn : Xm+1. When Xn=Xm=XZR, this is CSET.
func (a *Assembler) CSINC(rd, rn, rm Reg, cond Cond) {
	// 1|00|11010100|Rm|cond|01|Rn|Rd
	a.emit(0x9A800400 | uint32(rm)<<16 | uint32(cond)<<12 | uint32(rn)<<5 | uint32(rd))
}

// CSET: Xd = (cond) ? 1 : 0. Implemented as CSINC Xd, XZR, XZR, invert(cond).
func (a *Assembler) CSET(rd Reg, cond Cond) {
	// Invert condition code (flip bit 0)
	inv := cond ^ 1
	a.CSINC(rd, XZR, XZR, inv)
}

// LSLreg: Xd = Xn << Xm (register shift left, 64-bit)
func (a *Assembler) LSLreg(dst, src, amount Reg) { a.emit(0x9ac02000 | uint32(amount)<<16 | uint32(src)<<5 | uint32(dst)) }

// LSRreg: Xd = Xn >> Xm (register logical shift right, 64-bit)
func (a *Assembler) LSRreg(dst, src, amount Reg) { a.emit(0x9ac02400 | uint32(amount)<<16 | uint32(src)<<5 | uint32(dst)) }

// ORNreg: Xd = Xn | ~Xm (bitwise OR NOT, 64-bit)
func (a *Assembler) ORNreg(dst, src1, src2 Reg)  { a.emit(0xaa200000 | uint32(src2)<<16 | uint32(src1)<<5 | uint32(dst)) }
