package vm

// Opcode represents a bytecode instruction opcode.
type Opcode uint8

const (
	// Constants & Loads
	OP_LOADNIL  Opcode = iota // A B     : R(A)..R(A+B) = nil
	OP_LOADBOOL               // A B C   : R(A) = bool(B); if C then PC++
	OP_LOADINT                // A sBx   : R(A) = sBx (small signed integer)
	OP_LOADK                  // A Bx    : R(A) = Constants[Bx]

	// Variable Access
	OP_MOVE      // A B     : R(A) = R(B)
	OP_GETGLOBAL // A Bx    : R(A) = Globals[Constants[Bx]]
	OP_SETGLOBAL // A Bx    : Globals[Constants[Bx]] = R(A)
	OP_GETUPVAL  // A B     : R(A) = Upvalues[B]
	OP_SETUPVAL  // A B     : Upvalues[B] = R(A)

	// Table Operations
	OP_NEWTABLE // A B C   : R(A) = new table (B=array hint, C=hash hint)
	OP_GETTABLE // A B C   : R(A) = R(B)[RK(C)]
	OP_SETTABLE // A B C   : R(A)[RK(B)] = RK(C)
	OP_GETFIELD // A B Bx  : R(A) = R(B).Constants[Bx]  (optimized string field)
	OP_SETFIELD // A Bx C  : R(A).Constants[Bx] = RK(C)
	OP_SETLIST  // A B C   : R(A)[(C-1)*50+1..] = R(A+1)..R(A+B) (array init)
	OP_APPEND   // A B     : table.insert(R(A), R(B)) — for array-style table init

	// Arithmetic
	OP_ADD    // A B C : R(A) = RK(B) + RK(C)
	OP_SUB    // A B C : R(A) = RK(B) - RK(C)
	OP_MUL    // A B C : R(A) = RK(B) * RK(C)
	OP_DIV    // A B C : R(A) = RK(B) / RK(C)
	OP_MOD    // A B C : R(A) = RK(B) % RK(C)
	OP_POW    // A B C : R(A) = RK(B) ** RK(C)
	OP_UNM    // A B   : R(A) = -R(B)
	OP_NOT    // A B   : R(A) = !R(B)
	OP_LEN    // A B   : R(A) = #R(B)
	OP_CONCAT // A B C : R(A) = R(B) .. R(B+1) .. ... .. R(C)

	// Comparison (skip next instruction if test fails)
	OP_EQ // A B C : if (RK(B) == RK(C)) != bool(A) then PC++
	OP_LT // A B C : if (RK(B) <  RK(C)) != bool(A) then PC++
	OP_LE // A B C : if (RK(B) <= RK(C)) != bool(A) then PC++

	// Logical test (for short-circuit && / ||)
	OP_TEST    // A   C : if bool(R(A)) != bool(C) then PC++
	OP_TESTSET // A B C : if bool(R(B)) != bool(C) then PC++ else R(A) = R(B)

	// Jumps
	OP_JMP // sBx : PC += sBx

	// Calls & Returns
	OP_CALL    // A B C : R(A)..R(A+C-2) = R(A)(R(A+1)..R(A+B-1)); B=0 use top; C=0 return all
	OP_RETURN  // A B   : return R(A)..R(A+B-2); B=0 return to top; B=1 return nothing

	// Closure & Upvalue
	OP_CLOSURE // A Bx : R(A) = closure(Protos[Bx])
	OP_CLOSE   // A    : close upvalues >= R(A)

	// Numeric For Loop (optimized)
	OP_FORPREP // A sBx : R(A) -= R(A+2); PC += sBx
	OP_FORLOOP // A sBx : R(A) += R(A+2); if R(A) <?= R(A+1) { PC += sBx; R(A+3) = R(A) }

	// Generic For / Iterator
	OP_TFORCALL // A C   : R(A+3)..R(A+2+C) = R(A)(R(A+1), R(A+2))
	OP_TFORLOOP // A sBx : if R(A+1) != nil { R(A) = R(A+1); PC += sBx }

	// Varargs
	OP_VARARG // A B : R(A)..R(A+B-2) = varargs; B=0 all

	// Method call
	OP_SELF // A B C : R(A+1) = R(B); R(A) = R(B)[RK(C)]

	OP_MAX // sentinel
)

// Instruction encoding/decoding.
// 32-bit instruction word:
//
// Format ABC:  [op:8][A:8][B:8][C:8]
// Format ABx:  [op:8][A:8][Bx:16]       (Bx unsigned)
// Format AsBx: [op:8][A:8][sBx:16]      (sBx = Bx - 32767)
// Format sBx:  [op:8][_:8][sBx:16]      (for OP_JMP, A unused)

const sBxBias = 32767 // offset for signed Bx encoding

// RK encoding: if idx >= rkBit, it's a constant index (idx - rkBit).
const RKBit = 256

// EncodeABC creates an instruction in ABC format.
func EncodeABC(op Opcode, a, b, c int) uint32 {
	return uint32(op) | uint32(a&0xFF)<<8 | uint32(b&0xFF)<<16 | uint32(c&0xFF)<<24
}

// EncodeABx creates an instruction in ABx format (unsigned Bx).
func EncodeABx(op Opcode, a, bx int) uint32 {
	return uint32(op) | uint32(a&0xFF)<<8 | uint32(bx&0xFFFF)<<16
}

// EncodeAsBx creates an instruction in AsBx format (signed Bx).
func EncodeAsBx(op Opcode, a, sbx int) uint32 {
	return EncodeABx(op, a, sbx+sBxBias)
}

// EncodesBx creates a sBx-only instruction (A=0), used for OP_JMP.
func EncodesBx(op Opcode, sbx int) uint32 {
	return EncodeAsBx(op, 0, sbx)
}

// DecodeOp extracts the opcode from an instruction.
func DecodeOp(inst uint32) Opcode {
	return Opcode(inst & 0xFF)
}

// DecodeA extracts the A field (bits 8-15).
func DecodeA(inst uint32) int {
	return int((inst >> 8) & 0xFF)
}

// DecodeB extracts the B field (bits 16-23).
func DecodeB(inst uint32) int {
	return int((inst >> 16) & 0xFF)
}

// DecodeC extracts the C field (bits 24-31).
func DecodeC(inst uint32) int {
	return int((inst >> 24) & 0xFF)
}

// DecodeBx extracts the unsigned Bx field (bits 16-31).
func DecodeBx(inst uint32) int {
	return int((inst >> 16) & 0xFFFF)
}

// DecodesBx extracts the signed sBx field.
func DecodesBx(inst uint32) int {
	return DecodeBx(inst) - sBxBias
}

// IsRK returns true if the index refers to a constant (>= RKBit).
func IsRK(idx int) bool {
	return idx >= RKBit
}

// RKToConstIdx converts an RK index to a constant pool index.
func RKToConstIdx(rk int) int {
	return rk - RKBit
}

// ConstToRK converts a constant pool index to an RK index.
func ConstToRK(idx int) int {
	return idx + RKBit
}

// Opcode names for debugging.
var opNames = [...]string{
	OP_LOADNIL:  "LOADNIL",
	OP_LOADBOOL: "LOADBOOL",
	OP_LOADINT:  "LOADINT",
	OP_LOADK:    "LOADK",
	OP_MOVE:     "MOVE",
	OP_GETGLOBAL: "GETGLOBAL",
	OP_SETGLOBAL: "SETGLOBAL",
	OP_GETUPVAL: "GETUPVAL",
	OP_SETUPVAL: "SETUPVAL",
	OP_NEWTABLE: "NEWTABLE",
	OP_GETTABLE: "GETTABLE",
	OP_SETTABLE: "SETTABLE",
	OP_GETFIELD: "GETFIELD",
	OP_SETFIELD: "SETFIELD",
	OP_SETLIST:  "SETLIST",
	OP_APPEND:   "APPEND",
	OP_ADD:      "ADD",
	OP_SUB:      "SUB",
	OP_MUL:      "MUL",
	OP_DIV:      "DIV",
	OP_MOD:      "MOD",
	OP_POW:      "POW",
	OP_UNM:      "UNM",
	OP_NOT:      "NOT",
	OP_LEN:      "LEN",
	OP_CONCAT:   "CONCAT",
	OP_EQ:       "EQ",
	OP_LT:       "LT",
	OP_LE:       "LE",
	OP_TEST:     "TEST",
	OP_TESTSET:  "TESTSET",
	OP_JMP:      "JMP",
	OP_CALL:     "CALL",
	OP_RETURN:   "RETURN",
	OP_CLOSURE:  "CLOSURE",
	OP_CLOSE:    "CLOSE",
	OP_FORPREP:  "FORPREP",
	OP_FORLOOP:  "FORLOOP",
	OP_TFORCALL: "TFORCALL",
	OP_TFORLOOP: "TFORLOOP",
	OP_VARARG:   "VARARG",
	OP_SELF:     "SELF",
}

// OpName returns the name of an opcode.
func OpName(op Opcode) string {
	if int(op) < len(opNames) {
		return opNames[op]
	}
	return "???"
}
