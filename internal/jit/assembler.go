package jit

import (
	"encoding/binary"
	"fmt"
)

// ARM64 register names (X-form = 64-bit, W-form = 32-bit).
type Reg uint8

const (
	X0  Reg = 0
	X1  Reg = 1
	X2  Reg = 2
	X3  Reg = 3
	X4  Reg = 4
	X5  Reg = 5
	X6  Reg = 6
	X7  Reg = 7
	X8  Reg = 8
	X9  Reg = 9
	X10 Reg = 10
	X11 Reg = 11
	X12 Reg = 12
	X13 Reg = 13
	X14 Reg = 14
	X15 Reg = 15
	X16 Reg = 16
	X17 Reg = 17
	X18 Reg = 18
	X19 Reg = 19
	X20 Reg = 20
	X21 Reg = 21
	X22 Reg = 22
	X23 Reg = 23
	X24 Reg = 24
	X25 Reg = 25
	X26 Reg = 26
	X27 Reg = 27
	X28 Reg = 28
	X29 Reg = 29 // frame pointer
	X30 Reg = 30 // link register (LR)
	XZR Reg = 31 // zero register / SP depending on context
	SP  Reg = 31 // stack pointer (same encoding as XZR)
)

// W-form aliases (just documentation; same register encoding).
const (
	W0  = X0
	W1  = X1
	W2  = X2
	W3  = X3
	W15 = X15
)

// FP/SIMD register names.
type FReg uint8

const (
	D0  FReg = 0
	D1  FReg = 1
	D2  FReg = 2
	D3  FReg = 3
	D4  FReg = 4
	D5  FReg = 5
	D6  FReg = 6
	D7  FReg = 7
	D8  FReg = 8  // callee-saved
	D9  FReg = 9  // callee-saved
	D10 FReg = 10 // callee-saved
	D11 FReg = 11 // callee-saved
	D12 FReg = 12 // callee-saved
	D13 FReg = 13 // callee-saved
	D14 FReg = 14 // callee-saved
	D15 FReg = 15 // callee-saved
)

// Condition codes for B.cond.
type Cond uint8

const (
	CondEQ Cond = 0x0 // equal (Z=1)
	CondNE Cond = 0x1 // not equal (Z=0)
	CondHS Cond = 0x2 // unsigned >= (C=1)
	CondLO Cond = 0x3 // unsigned < (C=0)
	CondMI Cond = 0x4 // negative (N=1)
	CondPL Cond = 0x5 // positive or zero (N=0)
	CondVS Cond = 0x6 // overflow (V=1)
	CondVC Cond = 0x7 // no overflow (V=0)
	CondHI Cond = 0x8 // unsigned > (C=1 && Z=0)
	CondLS Cond = 0x9 // unsigned <= (C=0 || Z=1)
	CondGE Cond = 0xA // signed >=
	CondLT Cond = 0xB // signed <
	CondGT Cond = 0xC // signed >
	CondLE Cond = 0xD // signed <=
	CondAL Cond = 0xE // always
)

// fixupKind describes what kind of branch fixup is needed.
type fixupKind int

const (
	fixupB     fixupKind = iota // B (26-bit signed offset)
	fixupBCond                  // B.cond (19-bit signed offset)
	fixupCBZ                    // CBZ/CBNZ (19-bit signed offset)
)

type fixup struct {
	offset int       // byte offset in buf where the instruction lives
	label  string    // target label
	kind   fixupKind // branch type
}

// Assembler emits ARM64 machine code into a byte buffer.
type Assembler struct {
	buf    []byte
	labels map[string]int // label name -> byte offset
	fixups []fixup
}

// NewAssembler creates a new ARM64 assembler.
func NewAssembler() *Assembler {
	return &Assembler{
		buf:    make([]byte, 0, 4096),
		labels: make(map[string]int),
	}
}

// Offset returns the current byte offset in the code buffer.
func (a *Assembler) Offset() int {
	return len(a.buf)
}

// LabelOffset returns the offset of a label, if it exists.
func (a *Assembler) LabelOffset(name string) (int, bool) {
	off, ok := a.labels[name]
	return off, ok
}

// Label defines a label at the current offset.
func (a *Assembler) Label(name string) {
	if _, exists := a.labels[name]; exists {
		panic(fmt.Sprintf("jit: duplicate label %q", name))
	}
	a.labels[name] = len(a.buf)
}

// emit appends a 32-bit instruction.
func (a *Assembler) emit(inst uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], inst)
	a.buf = append(a.buf, buf[:]...)
}

// Finalize resolves all forward references and returns the machine code.
func (a *Assembler) Finalize() ([]byte, error) {
	for _, f := range a.fixups {
		target, ok := a.labels[f.label]
		if !ok {
			return nil, fmt.Errorf("jit: unresolved label %q", f.label)
		}
		offset := (target - f.offset) >> 2 // offset in instructions
		inst := binary.LittleEndian.Uint32(a.buf[f.offset:])

		switch f.kind {
		case fixupB:
			// B: imm26 at bits [25:0]
			if offset < -(1<<25) || offset >= (1<<25) {
				return nil, fmt.Errorf("jit: branch offset %d out of range for B", offset)
			}
			inst = (inst & 0xFC000000) | (uint32(offset) & 0x03FFFFFF)

		case fixupBCond:
			// B.cond: imm19 at bits [23:5]
			if offset < -(1<<18) || offset >= (1<<18) {
				return nil, fmt.Errorf("jit: branch offset %d out of range for B.cond", offset)
			}
			inst = (inst & 0xFF00001F) | ((uint32(offset) & 0x7FFFF) << 5)

		case fixupCBZ:
			// CBZ/CBNZ: imm19 at bits [23:5]
			if offset < -(1<<18) || offset >= (1<<18) {
				return nil, fmt.Errorf("jit: branch offset %d out of range for CBZ/CBNZ", offset)
			}
			inst = (inst & 0xFF00001F) | ((uint32(offset) & 0x7FFFF) << 5)
		}

		binary.LittleEndian.PutUint32(a.buf[f.offset:], inst)
	}
	return a.buf, nil
}

// Code returns the raw byte buffer (before finalization, labels may be unresolved).
func (a *Assembler) Code() []byte {
	return a.buf
}

// ──────────────────────────────────────────────────────────────────────────────
// Integer Data Processing
// ──────────────────────────────────────────────────────────────────────────────

// ADDreg: Xd = Xn + Xm (64-bit)
func (a *Assembler) ADDreg(rd, rn, rm Reg) {
	// 1|00|01011|00|0|Rm|000000|Rn|Rd
	a.emit(0x8B000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// ADDimm: Xd = Xn + #imm12 (64-bit, no shift)
func (a *Assembler) ADDimm(rd, rn Reg, imm12 uint16) {
	// 1|00|100010|0|imm12|Rn|Rd
	a.emit(0x91000000 | uint32(imm12&0xFFF)<<10 | uint32(rn)<<5 | uint32(rd))
}

// ADDSimm: Xd = Xn + #imm12, shift 12 (64-bit)
func (a *Assembler) ADDSimmS12(rd, rn Reg, imm12 uint16) {
	// 1|00|100010|1|imm12|Rn|Rd   (shift=1 means LSL #12)
	a.emit(0x91400000 | uint32(imm12&0xFFF)<<10 | uint32(rn)<<5 | uint32(rd))
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

// ADDSreg: ADDS Xd, Xn, Xm (sets flags). Used for overflow detection.
func (a *Assembler) ADDSreg(rd, rn, rm Reg) {
	// 1|01|01011|00|0|Rm|000000|Rn|Rd
	a.emit(0xAB000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
}

// SUBSreg: SUBS Xd, Xn, Xm (sets flags).
func (a *Assembler) SUBSreg(rd, rn, rm Reg) {
	// 1|11|01011|00|0|Rm|000000|Rn|Rd
	a.emit(0xEB000000 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rd))
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

// TSTimmPow2: TST Xn, #(1 << bit). Tests a single bit.
// This uses ANDS with bitmask immediate encoding.
func (a *Assembler) TSTimmPow2(rn Reg, bit uint8) {
	// ANDS XZR, Xn, #imm
	// For a single-bit bitmask, we need the logical immediate encoding.
	// immr = (64 - bit) & 63, imms = 0, N = 1
	immr := (64 - uint32(bit)) & 63
	imms := uint32(0)
	N := uint32(1)
	// 1|11|100100|N|immr|imms|Rn|Rd=11111
	a.emit(0xF200001F | N<<22 | immr<<16 | imms<<10 | uint32(rn)<<5)
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

// LDRpost: Xt = [Xn], #simm9 (post-index load, updates Xn)
func (a *Assembler) LDRpost(rt, rn Reg, simm9 int) {
	// LDR Xt, [Xn], #simm9: 1|1|11|1|00|01|0|imm9|01|Rn|Rt
	imm9 := uint32(simm9) & 0x1FF
	a.emit(0xF8400400 | imm9<<12 | uint32(rn)<<5 | uint32(rt))
}

// LDRreg: Xt = [Xn + Xm] (register offset load, 64-bit)
func (a *Assembler) LDRreg(rt, rn, rm Reg) {
	// LDR Xt, [Xn, Xm]: 11|11|1|00|01|1|Rm|011|0|10|Rn|Rt  (LSL #3)
	a.emit(0xF8606800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rt))
}

// LDRBreg: Wt = [Xn + Xm] (register offset byte load)
func (a *Assembler) LDRBreg(rt, rn, rm Reg) {
	// LDRB Wt, [Xn, Xm]: 00|11|1|00|01|1|Rm|011|0|10|Rn|Rt
	a.emit(0x38606800 | uint32(rm)<<16 | uint32(rn)<<5 | uint32(rt))
}

// LSLimm: Rd = Rn << shift (alias of UBFM)
func (a *Assembler) LSLimm(rd, rn Reg, shift uint8) {
	// LSL Xd, Xn, #shift = UBFM Xd, Xn, #(64-shift), #(63-shift)
	immr := uint32(64-shift) & 63
	imms := uint32(63-shift) & 63
	a.emit(0xD3400000 | immr<<16 | imms<<10 | uint32(rn)<<5 | uint32(rd))
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

// LDRW: Wt = [Xn + #offset] (32-bit load, unsigned offset, 4-byte aligned)
func (a *Assembler) LDRW(rt, rn Reg, offset int) {
	// LDR Wt, [Xn, #pimm]: 1|0|11|1|00|01|0|imm12|Rn|Rt
	pimm := offset >> 2
	a.emit(0xB9400000 | uint32(pimm&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
}

// STRW: [Xn + #offset] = Wt (32-bit store, unsigned offset, 4-byte aligned)
func (a *Assembler) STRW(rt, rn Reg, offset int) {
	// STR Wt, [Xn, #pimm]: 1|0|11|1|00|00|0|imm12|Rn|Rt
	pimm := offset >> 2
	a.emit(0xB9000000 | uint32(pimm&0xFFF)<<10 | uint32(rn)<<5 | uint32(rt))
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

// ──────────────────────────────────────────────────────────────────────────────
// Branch Instructions
// ──────────────────────────────────────────────────────────────────────────────

// B: Unconditional branch to label.
func (a *Assembler) B(label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupB})
	a.emit(0x14000000) // placeholder
}

// BOffset: Unconditional branch by instruction offset (already resolved).
func (a *Assembler) BOffset(instrOffset int) {
	a.emit(0x14000000 | (uint32(instrOffset) & 0x03FFFFFF))
}

// BCond: Conditional branch to label.
func (a *Assembler) BCond(cond Cond, label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupBCond})
	a.emit(0x54000000 | uint32(cond)) // placeholder
}

// BCondOffset: Conditional branch by instruction offset (already resolved).
func (a *Assembler) BCondOffset(cond Cond, instrOffset int) {
	a.emit(0x54000000 | ((uint32(instrOffset) & 0x7FFFF) << 5) | uint32(cond))
}

// CBZ: Compare and branch if zero (64-bit).
func (a *Assembler) CBZ(rt Reg, label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupCBZ})
	a.emit(0xB4000000 | uint32(rt)) // placeholder
}

// CBNZ: Compare and branch if not zero (64-bit).
func (a *Assembler) CBNZ(rt Reg, label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupCBZ})
	a.emit(0xB5000000 | uint32(rt)) // placeholder
}

// BL: Branch with link to label (function call).
func (a *Assembler) BL(label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupB})
	a.emit(0x94000000) // placeholder
}

// BLR: Branch with link to register (indirect call).
func (a *Assembler) BLR(rn Reg) {
	// 1|10|1011|0|0|01|11111|0000|00|Rn|00000
	a.emit(0xD63F0000 | uint32(rn)<<5)
}

// BR: Branch to register (indirect jump).
func (a *Assembler) BR(rn Reg) {
	// 1|10|1011|0|0|00|11111|0000|00|Rn|00000
	a.emit(0xD61F0000 | uint32(rn)<<5)
}

// RET: Return (branch to X30/LR).
func (a *Assembler) RET() {
	a.emit(0xD65F03C0) // RET X30
}

// NOP: No operation.
func (a *Assembler) NOP() {
	a.emit(0xD503201F)
}

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

// FSQRTd: Dd = sqrt(Dn) (double)
func (a *Assembler) FSQRTd(rd, rn FReg) {
	// 0|00|11110|01|1|00001|11000|Rn|Rd
	a.emit(0x1E61C000 | uint32(rn)<<5 | uint32(rd))
}

// FNEGd: Dd = -Dn (double)
func (a *Assembler) FNEGd(rd, rn FReg) {
	// 0|00|11110|01|1|000001|10000|Rn|Rd
	a.emit(0x1E614000 | uint32(rn)<<5 | uint32(rd))
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

// ──────────────────────────────────────────────────────────────────────────────
// ADR: form PC-relative address
// ──────────────────────────────────────────────────────────────────────────────

// ADRP: Xd = page address of label (used with ADD for full address).
// Not typically needed for JIT since we use absolute addresses via LoadImm64.

// ──────────────────────────────────────────────────────────────────────────────
// Conditional select
// ──────────────────────────────────────────────────────────────────────────────

// CSEL: Xd = (cond) ? Xn : Xm
func (a *Assembler) CSEL(rd, rn, rm Reg, cond Cond) {
	// 1|00|11010100|Rm|cond|00|Rn|Rd
	a.emit(0x9A800000 | uint32(rm)<<16 | uint32(cond)<<12 | uint32(rn)<<5 | uint32(rd))
}

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

// ──────────────────────────────────────────────────────────────────────────────
// System instructions
// ──────────────────────────────────────────────────────────────────────────────

// DMB ISH: Data memory barrier (inner shareable)
func (a *Assembler) DMB() {
	a.emit(0xD5033BBF)
}

// ISB: Instruction synchronization barrier
func (a *Assembler) ISB() {
	a.emit(0xD5033FDF)
}
