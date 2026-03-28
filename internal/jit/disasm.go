//go:build darwin && arm64

package jit

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unsafe"
)

// DumpARM64 returns a disassembly of the compiled trace's machine code.
// On macOS, wraps the code in a minimal Mach-O object and uses
// `xcrun llvm-objdump` for full disassembly. Falls back to raw hex
// with basic instruction decoding if llvm-objdump is not available.
func DumpARM64(ct *CompiledTrace) string {
	if ct == nil || ct.code == nil || ct.code.Size() == 0 {
		return "(no code)"
	}
	codeBytes := unsafe.Slice((*byte)(ct.code.Ptr()), ct.code.Size())

	// Try llvm-objdump first (available with Xcode command line tools)
	if result, err := disasmWithObjdump(codeBytes); err == nil {
		return result
	}

	// Fallback: raw hex + basic ARM64 decode
	return disasmRawHex(codeBytes)
}

// DumpARM64Bytes returns a disassembly of raw ARM64 code bytes.
func DumpARM64Bytes(code []byte) string {
	if len(code) == 0 {
		return "(no code)"
	}
	if result, err := disasmWithObjdump(code); err == nil {
		return result
	}
	return disasmRawHex(code)
}

// buildMachO creates a minimal Mach-O arm64 object file wrapping the given code bytes.
// This allows llvm-objdump to disassemble raw JIT code.
func buildMachO(code []byte) []byte {
	// Mach-O 64-bit header: 32 bytes
	// LC_SEGMENT_64 command: 72 bytes
	// Section header (__TEXT,__text): 80 bytes
	// Total header: 32 + 72 + 80 = 184 bytes

	const (
		headerSize  = 32
		segCmdSize  = 72
		sectSize    = 80
		totalHeader = headerSize + segCmdSize + sectSize
	)

	codeOff := totalHeader
	totalSize := totalHeader + len(code)

	buf := make([]byte, totalSize)

	// Mach-O 64-bit header
	binary.LittleEndian.PutUint32(buf[0:], 0xFEEDFACF)  // magic (MH_MAGIC_64)
	binary.LittleEndian.PutUint32(buf[4:], 0x0100000C)   // cputype (CPU_TYPE_ARM64)
	binary.LittleEndian.PutUint32(buf[8:], 0)             // cpusubtype
	binary.LittleEndian.PutUint32(buf[12:], 1)            // filetype (MH_OBJECT)
	binary.LittleEndian.PutUint32(buf[16:], 1)            // ncmds
	binary.LittleEndian.PutUint32(buf[20:], segCmdSize+sectSize) // sizeofcmds
	binary.LittleEndian.PutUint32(buf[24:], 0)            // flags
	binary.LittleEndian.PutUint32(buf[28:], 0)            // reserved

	// LC_SEGMENT_64 command
	off := headerSize
	binary.LittleEndian.PutUint32(buf[off:], 0x19) // cmd (LC_SEGMENT_64)
	binary.LittleEndian.PutUint32(buf[off+4:], segCmdSize+sectSize) // cmdsize
	// segname: 16 bytes of zeros (unnamed segment)
	// vmaddr (8 bytes at off+24)
	binary.LittleEndian.PutUint64(buf[off+24:], 0)
	// vmsize
	binary.LittleEndian.PutUint64(buf[off+32:], uint64(len(code)))
	// fileoff
	binary.LittleEndian.PutUint64(buf[off+40:], uint64(codeOff))
	// filesize
	binary.LittleEndian.PutUint64(buf[off+48:], uint64(len(code)))
	// maxprot
	binary.LittleEndian.PutUint32(buf[off+56:], 5) // r-x
	// initprot
	binary.LittleEndian.PutUint32(buf[off+60:], 5) // r-x
	// nsects
	binary.LittleEndian.PutUint32(buf[off+64:], 1)
	// flags
	binary.LittleEndian.PutUint32(buf[off+68:], 0)

	// Section header
	soff := headerSize + segCmdSize
	copy(buf[soff:], "__text\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00") // sectname (16 bytes)
	copy(buf[soff+16:], "__TEXT\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00") // segname (16 bytes)
	binary.LittleEndian.PutUint64(buf[soff+32:], 0)                // addr
	binary.LittleEndian.PutUint64(buf[soff+40:], uint64(len(code))) // size
	binary.LittleEndian.PutUint32(buf[soff+48:], uint32(codeOff))  // offset
	binary.LittleEndian.PutUint32(buf[soff+52:], 2)                // align (2^2=4)
	binary.LittleEndian.PutUint32(buf[soff+56:], 0)                // reloff
	binary.LittleEndian.PutUint32(buf[soff+60:], 0)                // nreloc
	binary.LittleEndian.PutUint32(buf[soff+64:], 0x80000400)       // flags: S_ATTR_PURE_INSTRUCTIONS|S_ATTR_SOME_INSTRUCTIONS
	binary.LittleEndian.PutUint32(buf[soff+68:], 0)                // reserved1
	binary.LittleEndian.PutUint32(buf[soff+72:], 0)                // reserved2
	binary.LittleEndian.PutUint32(buf[soff+76:], 0)                // reserved3

	// Code
	copy(buf[codeOff:], code)

	return buf
}

// disasmWithObjdump wraps code in a Mach-O object and disassembles with xcrun llvm-objdump.
func disasmWithObjdump(code []byte) (string, error) {
	macho := buildMachO(code)

	f, err := os.CreateTemp("", "jit-*.o")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(macho); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	// Try xcrun llvm-objdump
	out, err := exec.Command("xcrun", "llvm-objdump", "-d", f.Name()).CombinedOutput()
	if err != nil {
		// Try direct llvm-objdump
		out, err = exec.Command("llvm-objdump", "-d", f.Name()).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("llvm-objdump not available: %v", err)
		}
	}

	return cleanObjdumpOutput(string(out)), nil
}

// cleanObjdumpOutput strips file headers and section labels from llvm-objdump output,
// keeping only the disassembled instruction lines.
func cleanObjdumpOutput(raw string) string {
	var sb strings.Builder
	inCode := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Disassembly") {
			inCode = true
			continue
		}
		if !inCode {
			continue
		}
		// Skip section labels (e.g., "<__text>:")
		if strings.HasSuffix(trimmed, ":") && strings.HasPrefix(trimmed, "<") {
			continue
		}
		if trimmed == "" {
			continue
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// disasmRawHex produces a hex dump with basic ARM64 instruction decoding.
func disasmRawHex(code []byte) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== ARM64 Disassembly (%d bytes, %d instructions) ===\n",
		len(code), len(code)/4))
	for i := 0; i+4 <= len(code); i += 4 {
		inst := binary.LittleEndian.Uint32(code[i : i+4])
		decoded := decodeARM64(inst)
		sb.WriteString(fmt.Sprintf("  %04x: %08x  %s\n", i, inst, decoded))
	}
	return sb.String()
}

// decodeARM64 performs basic ARM64 instruction decoding.
// This is a simplified decoder for common instructions seen in JIT output.
func decodeARM64(inst uint32) string {
	// NOP
	if inst == 0xd503201f {
		return "nop"
	}

	// RET (0xd65f03c0)
	if inst == 0xd65f03c0 {
		return "ret"
	}

	// B (unconditional branch): 000101 imm26
	if inst>>26 == 0x05 {
		imm26 := int32(inst&0x03FFFFFF) << 6 >> 6 // sign-extend
		return fmt.Sprintf("b  #%+d", imm26*4)
	}

	// BL (branch with link): 100101 imm26
	if inst>>26 == 0x25 {
		imm26 := int32(inst&0x03FFFFFF) << 6 >> 6
		return fmt.Sprintf("bl  #%+d", imm26*4)
	}

	// B.cond: 01010100 imm19 0 cond
	if inst>>24 == 0x54 {
		cond := inst & 0xF
		imm19 := int32((inst>>5)&0x7FFFF) << 13 >> 13
		condStr := condName(cond)
		return fmt.Sprintf("b.%s  #%+d", condStr, imm19*4)
	}

	// CBZ: 10110100 imm19 Rt (64-bit)
	if inst>>24 == 0xb4 {
		rt := inst & 0x1F
		imm19 := int32((inst>>5)&0x7FFFF) << 13 >> 13
		return fmt.Sprintf("cbz  x%d, #%+d", rt, imm19*4)
	}

	// CBNZ: 10110101 imm19 Rt (64-bit)
	if inst>>24 == 0xb5 {
		rt := inst & 0x1F
		imm19 := int32((inst>>5)&0x7FFFF) << 13 >> 13
		return fmt.Sprintf("cbnz  x%d, #%+d", rt, imm19*4)
	}

	// ADD (immediate, 64-bit): 1001000100 shift imm12 Rn Rd
	if inst>>22 == 0x244 {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		imm12 := (inst >> 10) & 0xFFF
		return fmt.Sprintf("add  x%d, x%d, #%d", rd, rn, imm12)
	}

	// SUB (immediate, 64-bit): 1101000100 shift imm12 Rn Rd
	if inst>>22 == 0x344 {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		imm12 := (inst >> 10) & 0xFFF
		return fmt.Sprintf("sub  x%d, x%d, #%d", rd, rn, imm12)
	}

	// ADD (shifted register, 64-bit): 10001011 shift 0 Rm imm6 Rn Rd
	if inst>>24 == 0x8b {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("add  x%d, x%d, x%d", rd, rn, rm)
	}

	// SUB (shifted register, 64-bit): 11001011 shift 0 Rm imm6 Rn Rd
	if inst>>24 == 0xcb {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("sub  x%d, x%d, x%d", rd, rn, rm)
	}

	// MUL: 10011011000 Rm 011111 Rn Rd
	if inst>>21 == 0x4d8 && ((inst>>10)&0x3F) == 0x1F {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("mul  x%d, x%d, x%d", rd, rn, rm)
	}

	// LDR (unsigned offset, 64-bit): 11111001 01 imm12 Rn Rt
	if inst>>22 == 0x3E5 {
		rt := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		imm12 := (inst >> 10) & 0xFFF
		return fmt.Sprintf("ldr  x%d, [x%d, #%d]", rt, rn, imm12*8)
	}

	// STR (unsigned offset, 64-bit): 11111001 00 imm12 Rn Rt
	if inst>>22 == 0x3E4 {
		rt := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		imm12 := (inst >> 10) & 0xFFF
		return fmt.Sprintf("str  x%d, [x%d, #%d]", rt, rn, imm12*8)
	}

	// STP (pre-index, 64-bit): 10101001 10 imm7 Rt2 Rn Rt
	if inst>>22 == 0x2A6 {
		rt := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rt2 := (inst >> 10) & 0x1F
		imm7 := int32((inst>>15)&0x7F) << 25 >> 25
		return fmt.Sprintf("stp  x%d, x%d, [x%d, #%d]!", rt, rt2, rn, imm7*8)
	}

	// STP (signed offset, 64-bit): 10101001 00 imm7 Rt2 Rn Rt
	if inst>>22 == 0x2A4 {
		rt := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rt2 := (inst >> 10) & 0x1F
		imm7 := int32((inst>>15)&0x7F) << 25 >> 25
		return fmt.Sprintf("stp  x%d, x%d, [x%d, #%d]", rt, rt2, rn, imm7*8)
	}

	// LDP (post-index, 64-bit): 10101000 11 imm7 Rt2 Rn Rt
	if inst>>22 == 0x2A3 {
		rt := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rt2 := (inst >> 10) & 0x1F
		imm7 := int32((inst>>15)&0x7F) << 25 >> 25
		return fmt.Sprintf("ldp  x%d, x%d, [x%d], #%d", rt, rt2, rn, imm7*8)
	}

	// LDP (signed offset, 64-bit): 10101001 01 imm7 Rt2 Rn Rt
	if inst>>22 == 0x2A5 {
		rt := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rt2 := (inst >> 10) & 0x1F
		imm7 := int32((inst>>15)&0x7F) << 25 >> 25
		return fmt.Sprintf("ldp  x%d, x%d, [x%d, #%d]", rt, rt2, rn, imm7*8)
	}

	// MOV (register, 64-bit) = ORR Xd, XZR, Xm: 10101010000 Rm 000000 11111 Rd
	if inst>>21 == 0x550 && ((inst>>5)&0x1F) == 31 && ((inst>>10)&0x3F) == 0 {
		rd := inst & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("mov  x%d, x%d", rd, rm)
	}

	// MOVZ (64-bit): 110100101 hw imm16 Rd
	if inst>>23 == 0x1A5 {
		rd := inst & 0x1F
		imm16 := (inst >> 5) & 0xFFFF
		hw := (inst >> 21) & 0x3
		return fmt.Sprintf("movz  x%d, #%d, lsl #%d", rd, imm16, hw*16)
	}

	// MOVK (64-bit): 111100101 hw imm16 Rd
	if inst>>23 == 0x1E5 {
		rd := inst & 0x1F
		imm16 := (inst >> 5) & 0xFFFF
		hw := (inst >> 21) & 0x3
		return fmt.Sprintf("movk  x%d, #%d, lsl #%d", rd, imm16, hw*16)
	}

	// CMP (immediate): SUBS XZR, Xn, #imm = 1111000100 shift imm12 Rn 11111
	if inst>>22 == 0x3C4 && (inst&0x1F) == 31 {
		rn := (inst >> 5) & 0x1F
		imm12 := (inst >> 10) & 0xFFF
		return fmt.Sprintf("cmp  x%d, #%d", rn, imm12)
	}

	// CMP (shifted register): SUBS XZR, Xn, Xm = 11101011 shift 0 Rm imm6 Rn 11111
	if inst>>24 == 0xeb && (inst&0x1F) == 31 {
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("cmp  x%d, x%d", rn, rm)
	}

	// ORR (immediate, 64-bit): used for NaN-boxing tag construction
	if inst>>23 == 0x164 {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		return fmt.Sprintf("orr  x%d, x%d, #<imm>", rd, rn)
	}

	// FADD (double): 00011110 011 Rm 001010 Rn Rd
	if inst>>21 == 0xF1 && ((inst>>10)&0x3F) == 0x0A {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("fadd  d%d, d%d, d%d", rd, rn, rm)
	}

	// FSUB (double): 00011110 011 Rm 001110 Rn Rd
	if inst>>21 == 0xF1 && ((inst>>10)&0x3F) == 0x0E {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("fsub  d%d, d%d, d%d", rd, rn, rm)
	}

	// FMUL (double): 00011110 011 Rm 000010 Rn Rd
	if inst>>21 == 0xF1 && ((inst>>10)&0x3F) == 0x02 {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("fmul  d%d, d%d, d%d", rd, rn, rm)
	}

	// FDIV (double): 00011110 011 Rm 000110 Rn Rd
	if inst>>21 == 0xF1 && ((inst>>10)&0x3F) == 0x06 {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		rm := (inst >> 16) & 0x1F
		return fmt.Sprintf("fdiv  d%d, d%d, d%d", rd, rn, rm)
	}

	// FMOV (general to FP, 64-bit): 10011110 01 1 00111 000000 Rn Rd
	if inst>>16 == 0x9E67 {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		return fmt.Sprintf("fmov  d%d, x%d", rd, rn)
	}

	// LSR (immediate, 64-bit) = UBFM: 1101001101 immr imms Rn Rd
	if inst>>22 == 0x34D {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		imms := (inst >> 10) & 0x3F
		immr := (inst >> 16) & 0x3F
		if imms == 63 {
			return fmt.Sprintf("lsr  x%d, x%d, #%d", rd, rn, immr)
		}
		return fmt.Sprintf("ubfm  x%d, x%d, #%d, #%d", rd, rn, immr, imms)
	}

	// ASR (immediate, 64-bit) = SBFM: 1001001101 immr imms Rn Rd
	if inst>>22 == 0x24D && ((inst>>10)&0x3F) == 63 {
		rd := inst & 0x1F
		rn := (inst >> 5) & 0x1F
		immr := (inst >> 16) & 0x3F
		return fmt.Sprintf("asr  x%d, x%d, #%d", rd, rn, immr)
	}

	return fmt.Sprintf("??? (%08x)", inst)
}

// condName returns the ARM64 condition code name.
func condName(cond uint32) string {
	names := [16]string{
		"eq", "ne", "hs", "lo", "mi", "pl", "vs", "vc",
		"hi", "ls", "ge", "lt", "gt", "le", "al", "nv",
	}
	if cond < 16 {
		return names[cond]
	}
	return fmt.Sprintf("cond%d", cond)
}
