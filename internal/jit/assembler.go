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
// ADR: form PC-relative address
// ──────────────────────────────────────────────────────────────────────────────

// ADRP: Xd = page address of label (used with ADD for full address).
// Not typically needed for JIT since we use absolute addresses via LoadImm64.
