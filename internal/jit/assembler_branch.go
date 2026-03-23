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

