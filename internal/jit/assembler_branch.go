package jit

// ──────────────────────────────────────────────────────────────────────────────
// Branch Instructions
// ──────────────────────────────────────────────────────────────────────────────

// B: Unconditional branch to label.
func (a *Assembler) B(label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupB})
	a.emit(0x14000000) // placeholder
}

// BCond: Conditional branch to label.
func (a *Assembler) BCond(cond Cond, label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupBCond})
	a.emit(0x54000000 | uint32(cond)) // placeholder
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

// TBNZ: Test bit and branch if nonzero.
// Tests bit 'bit' of register rt and branches to label if the bit is 1.
// For 64-bit registers, bit can be 0-63.
func (a *Assembler) TBNZ(rt Reg, bit int, label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupTBZ})
	// TBNZ encoding: b5|0110111|b40|imm14|Rt
	// b5 = bit[5] (placed at bit 31), b40 = bit[4:0] (placed at bits [23:19])
	b5 := uint32((bit >> 5) & 1)
	b40 := uint32(bit & 0x1F)
	a.emit(b5<<31 | 0x37000000 | b40<<19 | uint32(rt))
}

// TBZ: Test bit and branch if zero.
// Tests bit 'bit' of register rt and branches to label if the bit is 0.
func (a *Assembler) TBZ(rt Reg, bit int, label string) {
	a.fixups = append(a.fixups, fixup{offset: len(a.buf), label: label, kind: fixupTBZ})
	// TBZ encoding: b5|0110110|b40|imm14|Rt
	b5 := uint32((bit >> 5) & 1)
	b40 := uint32(bit & 0x1F)
	a.emit(b5<<31 | 0x36000000 | b40<<19 | uint32(rt))
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
// System instructions
// ──────────────────────────────────────────────────────────────────────────────

