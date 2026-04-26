//go:build darwin && arm64

// warm_dump.go implements production-warm Tier 2 diagnostics.
//
// Unlike CompileForDiagnostics, this path does not compile a proto after the
// fact. A caller enables a WarmDumpSession before executing real code, and the
// production compileTier2 path captures artifacts only when the normal tiering
// machinery actually attempts Tier 2 for that workload.

package methodjit

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/arch/arm64/arm64asm"

	"github.com/gscript/gscript/internal/vm"
)

// WarmDumpSession records Tier 2 compile artifacts observed during a real run.
type WarmDumpSession struct {
	dir         string
	protoName   string
	mu          sync.Mutex
	records     map[*vm.FuncProto]*WarmDumpRecord
	nextAttempt int
}

// WarmDumpRecord is the captured artifact for one production Tier 2 attempt.
type WarmDumpRecord struct {
	Attempt        int
	ProtoName      string
	NumParams      int
	MaxStack       int
	IRBefore       string
	IRAfter        string
	IntrinsicNotes []string
	RegAllocMap    string
	CompiledCode   []byte
	InsnCount      int
	InsnHistogram  map[string]int
	DirectEntryOff int
	NumSpills      int
	CompileErr     string
}

type warmDumpManifest struct {
	ProtoFilter string                  `json:"proto_filter,omitempty"`
	Protos      []warmDumpProtoManifest `json:"protos"`
}

type warmDumpProtoManifest struct {
	Name           string              `json:"name"`
	Status         string              `json:"status"`
	Attempt        int                 `json:"attempt,omitempty"`
	Entered        bool                `json:"entered"`
	Compiled       bool                `json:"compiled"`
	Failed         bool                `json:"failed"`
	FailureReason  string              `json:"failure_reason,omitempty"`
	CallCount      int                 `json:"call_count"`
	Tier2Promoted  bool                `json:"tier2_promoted"`
	NumParams      int                 `json:"num_params"`
	MaxStack       int                 `json:"max_stack"`
	InsnCount      int                 `json:"insn_count,omitempty"`
	InsnHistogram  map[string]int      `json:"insn_histogram,omitempty"`
	CodeBytes      int                 `json:"code_bytes,omitempty"`
	DirectEntryOff int                 `json:"direct_entry_offset,omitempty"`
	NumSpills      int                 `json:"num_spills,omitempty"`
	Feedback       warmFeedbackSummary `json:"feedback"`
	Files          map[string]string   `json:"files,omitempty"`
}

type warmFeedbackSummary struct {
	Slots       int              `json:"slots"`
	Observed    int              `json:"observed"`
	Left        map[string]int   `json:"left"`
	Right       map[string]int   `json:"right"`
	Result      map[string]int   `json:"result"`
	Kind        map[string]int   `json:"kind"`
	ObservedPCs []warmFeedbackPC `json:"observed_pcs,omitempty"`
}

type warmFeedbackPC struct {
	PC     int    `json:"pc"`
	Op     string `json:"op"`
	Left   string `json:"left,omitempty"`
	Right  string `json:"right,omitempty"`
	Result string `json:"result,omitempty"`
	Kind   string `json:"kind,omitempty"`
}

// EnableWarmDump configures tm to capture artifacts from future production
// Tier 2 attempts. It must be called before executing the workload.
func (tm *TieringManager) EnableWarmDump(dir, protoName string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("warm dump directory is required")
	}
	tm.warmDump = &WarmDumpSession{
		dir:       dir,
		protoName: protoName,
		records:   make(map[*vm.FuncProto]*WarmDumpRecord),
	}
	return nil
}

func (tm *TieringManager) warmDumpTrace(proto *vm.FuncProto) *Tier2Trace {
	if tm == nil || tm.warmDump == nil || !tm.warmDump.matches(proto) {
		return nil
	}
	return &Tier2Trace{}
}

func (tm *TieringManager) recordWarmDumpCompile(proto *vm.FuncProto, trace *Tier2Trace, cf *CompiledFunction, compileErr error) {
	if tm == nil || tm.warmDump == nil || trace == nil || !tm.warmDump.matches(proto) {
		return
	}
	tm.warmDump.record(proto, trace, cf, compileErr)
}

func (s *WarmDumpSession) matches(proto *vm.FuncProto) bool {
	if s == nil || proto == nil {
		return false
	}
	if s.protoName == "" {
		return true
	}
	return proto.Name == s.protoName
}

func (s *WarmDumpSession) record(proto *vm.FuncProto, trace *Tier2Trace, cf *CompiledFunction, compileErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextAttempt++
	rec := &WarmDumpRecord{
		Attempt:        s.nextAttempt,
		ProtoName:      proto.Name,
		NumParams:      proto.NumParams,
		MaxStack:       proto.MaxStack,
		IRBefore:       trace.IRBefore,
		IRAfter:        trace.IRAfter,
		IntrinsicNotes: append([]string(nil), trace.IntrinsicNotes...),
		RegAllocMap:    trace.RegAllocMap,
	}
	if compileErr != nil {
		rec.CompileErr = compileErr.Error()
	}
	if cf != nil {
		rec.DirectEntryOff = cf.DirectEntryOffset
		rec.NumSpills = cf.NumSpills
		rec.CompiledCode = make([]byte, cf.Code.Size())
		copy(rec.CompiledCode, unsafeCodeSlice(cf))
		rec.InsnCount, rec.InsnHistogram = classifyARM64(rec.CompiledCode)
	}
	s.records[proto] = rec
}

// WriteWarmDump writes the warm artifacts captured so far. It walks the full
// proto tree so status and feedback are visible even for protos that never
// reached Tier 2 during the real workload.
func (tm *TieringManager) WriteWarmDump(top *vm.FuncProto) error {
	if tm == nil || tm.warmDump == nil {
		return nil
	}
	return tm.warmDump.write(tm, top)
}

func (s *WarmDumpSession) write(tm *TieringManager, top *vm.FuncProto) error {
	s.mu.Lock()
	records := make(map[*vm.FuncProto]*WarmDumpRecord, len(s.records))
	for proto, rec := range s.records {
		records[proto] = rec
	}
	s.mu.Unlock()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create warm dump dir: %w", err)
	}

	protos := collectWarmDumpProtos(top)
	manifest := warmDumpManifest{ProtoFilter: s.protoName}
	usedNames := make(map[string]int)

	for _, proto := range protos {
		if !s.matches(proto) {
			continue
		}
		rec := records[proto]
		base := uniqueWarmDumpBase(proto, usedNames)
		files := make(map[string]string)

		status := warmDumpStatus(tm, proto, rec)
		feedback := summarizeWarmFeedback(proto)

		feedbackName := base + ".feedback.txt"
		if err := os.WriteFile(filepath.Join(s.dir, feedbackName), []byte(formatWarmFeedback(proto, feedback)), 0o644); err != nil {
			return fmt.Errorf("write feedback for %s: %w", proto.Name, err)
		}
		files["feedback"] = feedbackName

		if rec != nil {
			if rec.IRBefore != "" {
				name := base + ".ir.before.txt"
				if err := os.WriteFile(filepath.Join(s.dir, name), []byte(rec.IRBefore), 0o644); err != nil {
					return fmt.Errorf("write IR-before for %s: %w", proto.Name, err)
				}
				files["ir_before"] = name
			}
			if rec.IRAfter != "" {
				name := base + ".ir.after.txt"
				if err := os.WriteFile(filepath.Join(s.dir, name), []byte(rec.IRAfter), 0o644); err != nil {
					return fmt.Errorf("write IR-after for %s: %w", proto.Name, err)
				}
				files["ir_after"] = name
			}
			if rec.RegAllocMap != "" {
				name := base + ".regalloc.txt"
				if err := os.WriteFile(filepath.Join(s.dir, name), []byte(rec.RegAllocMap), 0o644); err != nil {
					return fmt.Errorf("write regalloc for %s: %w", proto.Name, err)
				}
				files["regalloc"] = name
			}
			if len(rec.IntrinsicNotes) > 0 {
				name := base + ".intrinsics.txt"
				body := strings.Join(rec.IntrinsicNotes, "\n") + "\n"
				if err := os.WriteFile(filepath.Join(s.dir, name), []byte(body), 0o644); err != nil {
					return fmt.Errorf("write intrinsics for %s: %w", proto.Name, err)
				}
				files["intrinsics"] = name
			}
			if len(rec.CompiledCode) > 0 {
				binName := base + ".bin"
				if err := os.WriteFile(filepath.Join(s.dir, binName), rec.CompiledCode, 0o644); err != nil {
					return fmt.Errorf("write code for %s: %w", proto.Name, err)
				}
				files["bin"] = binName
				asmName := base + ".asm.txt"
				if err := os.WriteFile(filepath.Join(s.dir, asmName), []byte(disasmWarmARM64(rec.CompiledCode)), 0o644); err != nil {
					return fmt.Errorf("write asm for %s: %w", proto.Name, err)
				}
				files["asm"] = asmName
			}
		}

		protoManifest := warmDumpProtoManifest{
			Name:          displayWarmProtoName(proto),
			Status:        status.status,
			Entered:       proto.EnteredTier2 != 0,
			Compiled:      status.compiled,
			Failed:        status.failed,
			FailureReason: status.failureReason,
			CallCount:     proto.CallCount,
			Tier2Promoted: proto.Tier2Promoted,
			NumParams:     proto.NumParams,
			MaxStack:      proto.MaxStack,
			Feedback:      feedback,
			Files:         files,
		}
		if rec != nil {
			protoManifest.Attempt = rec.Attempt
			protoManifest.InsnCount = rec.InsnCount
			protoManifest.InsnHistogram = rec.InsnHistogram
			protoManifest.CodeBytes = len(rec.CompiledCode)
			protoManifest.DirectEntryOff = rec.DirectEntryOff
			protoManifest.NumSpills = rec.NumSpills
		}

		statusName := base + ".status.json"
		statusBytes, err := json.MarshalIndent(protoManifest, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal status for %s: %w", proto.Name, err)
		}
		statusBytes = append(statusBytes, '\n')
		if err := os.WriteFile(filepath.Join(s.dir, statusName), statusBytes, 0o644); err != nil {
			return fmt.Errorf("write status for %s: %w", proto.Name, err)
		}
		protoManifest.Files["status"] = statusName

		manifest.Protos = append(manifest.Protos, protoManifest)
	}

	sort.Slice(manifest.Protos, func(i, j int) bool {
		if manifest.Protos[i].Attempt != manifest.Protos[j].Attempt {
			if manifest.Protos[i].Attempt == 0 {
				return false
			}
			if manifest.Protos[j].Attempt == 0 {
				return true
			}
			return manifest.Protos[i].Attempt < manifest.Protos[j].Attempt
		}
		return manifest.Protos[i].Name < manifest.Protos[j].Name
	})

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal warm dump manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(s.dir, "manifest.json"), data, 0o644); err != nil {
		return fmt.Errorf("write warm dump manifest: %w", err)
	}
	return nil
}

type warmDumpStatusInfo struct {
	status        string
	compiled      bool
	failed        bool
	failureReason string
}

func warmDumpStatus(tm *TieringManager, proto *vm.FuncProto, rec *WarmDumpRecord) warmDumpStatusInfo {
	info := warmDumpStatusInfo{status: "not_attempted"}
	if rec != nil {
		info.status = "compiled"
		info.compiled = rec.CompileErr == ""
		if rec.CompileErr != "" {
			info.status = "failed"
			info.failed = true
			info.failureReason = rec.CompileErr
		}
	}
	if reason := tm.tier2FailReason[proto]; reason != "" {
		info.status = "failed"
		info.failed = true
		info.compiled = false
		info.failureReason = reason
	}
	if _, ok := tm.tier2Compiled[proto]; ok && !info.failed {
		info.status = "compiled"
		info.compiled = true
	}
	if proto.EnteredTier2 != 0 && !info.failed {
		info.status = "entered"
		info.compiled = true
	}
	return info
}

func collectWarmDumpProtos(top *vm.FuncProto) []*vm.FuncProto {
	var out []*vm.FuncProto
	var walk func(*vm.FuncProto)
	walk = func(proto *vm.FuncProto) {
		if proto == nil {
			return
		}
		out = append(out, proto)
		for _, sub := range proto.Protos {
			walk(sub)
		}
	}
	walk(top)
	return out
}

func uniqueWarmDumpBase(proto *vm.FuncProto, used map[string]int) string {
	base := sanitizeWarmName(displayWarmProtoName(proto))
	if base == "" {
		base = "_anon"
	}
	used[base]++
	if used[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, used[base])
}

func displayWarmProtoName(proto *vm.FuncProto) string {
	if proto == nil || proto.Name == "" {
		return "<main>"
	}
	return proto.Name
}

func sanitizeWarmName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == '<', r == '>', r == '.', r == '/', r == ' ':
			b.WriteByte('_')
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func summarizeWarmFeedback(proto *vm.FuncProto) warmFeedbackSummary {
	summary := warmFeedbackSummary{
		Left:   make(map[string]int),
		Right:  make(map[string]int),
		Result: make(map[string]int),
		Kind:   make(map[string]int),
	}
	if proto == nil {
		return summary
	}
	summary.Slots = len(proto.Feedback)
	for pc, fb := range proto.Feedback {
		left := feedbackTypeName(fb.Left)
		right := feedbackTypeName(fb.Right)
		result := feedbackTypeName(fb.Result)
		kind := feedbackKindName(fb.Kind)
		summary.Left[left]++
		summary.Right[right]++
		summary.Result[result]++
		summary.Kind[kind]++
		if fb.Left != vm.FBUnobserved || fb.Right != vm.FBUnobserved ||
			fb.Result != vm.FBUnobserved || fb.Kind != vm.FBKindUnobserved {
			summary.Observed++
			op := "?"
			if pc < len(proto.Code) {
				op = opcodeName(vm.DecodeOp(proto.Code[pc]))
			}
			summary.ObservedPCs = append(summary.ObservedPCs, warmFeedbackPC{
				PC:     pc,
				Op:     op,
				Left:   left,
				Right:  right,
				Result: result,
				Kind:   kind,
			})
		}
	}
	return summary
}

func formatWarmFeedback(proto *vm.FuncProto, summary warmFeedbackSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "proto: %s\n", displayWarmProtoName(proto))
	fmt.Fprintf(&b, "slots: %d\n", summary.Slots)
	fmt.Fprintf(&b, "observed: %d\n\n", summary.Observed)
	fmt.Fprintf(&b, "result: %s\n", formatWarmCounts(summary.Result))
	fmt.Fprintf(&b, "left:   %s\n", formatWarmCounts(summary.Left))
	fmt.Fprintf(&b, "right:  %s\n", formatWarmCounts(summary.Right))
	fmt.Fprintf(&b, "kind:   %s\n\n", formatWarmCounts(summary.Kind))
	b.WriteString("pc\top\tleft\tright\tresult\tkind\n")
	for _, pc := range summary.ObservedPCs {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%s\t%s\n", pc.PC, pc.Op, pc.Left, pc.Right, pc.Result, pc.Kind)
	}
	return b.String()
}

func formatWarmCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k, v := range counts {
		if v != 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, ", ")
}

func feedbackTypeName(t vm.FeedbackType) string {
	switch t {
	case vm.FBUnobserved:
		return "unobserved"
	case vm.FBInt:
		return "int"
	case vm.FBFloat:
		return "float"
	case vm.FBString:
		return "string"
	case vm.FBBool:
		return "bool"
	case vm.FBTable:
		return "table"
	case vm.FBFunction:
		return "function"
	case vm.FBAny:
		return "any"
	default:
		return fmt.Sprintf("feedback_%d", t)
	}
}

func feedbackKindName(k uint8) string {
	switch k {
	case vm.FBKindUnobserved:
		return "unobserved"
	case vm.FBKindMixed:
		return "mixed"
	case vm.FBKindInt:
		return "int"
	case vm.FBKindFloat:
		return "float"
	case vm.FBKindBool:
		return "bool"
	case vm.FBKindPolymorphic:
		return "polymorphic"
	default:
		return fmt.Sprintf("kind_%d", k)
	}
}

func opcodeName(op vm.Opcode) string {
	if int(op) >= 0 && int(op) < len(opcodeNames) && opcodeNames[op] != "" {
		return opcodeNames[op]
	}
	return fmt.Sprintf("OP_%d", op)
}

var opcodeNames = [...]string{
	vm.OP_LOADNIL:   "LOADNIL",
	vm.OP_LOADBOOL:  "LOADBOOL",
	vm.OP_LOADINT:   "LOADINT",
	vm.OP_LOADK:     "LOADK",
	vm.OP_MOVE:      "MOVE",
	vm.OP_GETGLOBAL: "GETGLOBAL",
	vm.OP_SETGLOBAL: "SETGLOBAL",
	vm.OP_GETUPVAL:  "GETUPVAL",
	vm.OP_SETUPVAL:  "SETUPVAL",
	vm.OP_NEWTABLE:  "NEWTABLE",
	vm.OP_GETTABLE:  "GETTABLE",
	vm.OP_SETTABLE:  "SETTABLE",
	vm.OP_GETFIELD:  "GETFIELD",
	vm.OP_SETFIELD:  "SETFIELD",
	vm.OP_SETLIST:   "SETLIST",
	vm.OP_APPEND:    "APPEND",
	vm.OP_ADD:       "ADD",
	vm.OP_SUB:       "SUB",
	vm.OP_MUL:       "MUL",
	vm.OP_DIV:       "DIV",
	vm.OP_MOD:       "MOD",
	vm.OP_POW:       "POW",
	vm.OP_UNM:       "UNM",
	vm.OP_NOT:       "NOT",
	vm.OP_LEN:       "LEN",
	vm.OP_CONCAT:    "CONCAT",
	vm.OP_EQ:        "EQ",
	vm.OP_LT:        "LT",
	vm.OP_LE:        "LE",
	vm.OP_TEST:      "TEST",
	vm.OP_TESTSET:   "TESTSET",
	vm.OP_JMP:       "JMP",
	vm.OP_CALL:      "CALL",
	vm.OP_RETURN:    "RETURN",
	vm.OP_CLOSURE:   "CLOSURE",
	vm.OP_CLOSE:     "CLOSE",
	vm.OP_FORPREP:   "FORPREP",
	vm.OP_FORLOOP:   "FORLOOP",
	vm.OP_TFORCALL:  "TFORCALL",
	vm.OP_TFORLOOP:  "TFORLOOP",
	vm.OP_VARARG:    "VARARG",
	vm.OP_SELF:      "SELF",
	vm.OP_GO:        "GO",
	vm.OP_MAKECHAN:  "MAKECHAN",
	vm.OP_SEND:      "SEND",
	vm.OP_RECV:      "RECV",
}

func disasmWarmARM64(code []byte) string {
	var b strings.Builder
	for i := 0; i+4 <= len(code); i += 4 {
		word := binary.LittleEndian.Uint32(code[i : i+4])
		inst, err := arm64asm.Decode(code[i : i+4])
		if err != nil {
			fmt.Fprintf(&b, "%04x  %08x  .word\n", i, word)
			continue
		}
		fmt.Fprintf(&b, "%04x  %08x  %s\n", i, word, arm64asm.GoSyntax(inst, 0, nil, nil))
	}
	return b.String()
}
