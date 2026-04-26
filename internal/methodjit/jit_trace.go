//go:build darwin && arm64

package methodjit

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/gscript/gscript/internal/vm"
)

const (
	JITTimelineJSONL = "jsonl"
	JITTimelineJSON  = "json"
)

// JITTimelineEvent is one production JIT timeline record.
//
// Event names are stable snake_case strings. Extra event-specific data lives
// in Attrs so new diagnostics can be added without changing the base schema.
type JITTimelineEvent struct {
	Seq      uint64         `json:"seq"`
	Time     string         `json:"time"`
	UnixNano int64          `json:"unix_nano"`
	Event    string         `json:"event"`
	Tier     string         `json:"tier,omitempty"`
	Proto    string         `json:"proto,omitempty"`
	ProtoID  string         `json:"proto_id,omitempty"`
	Attrs    map[string]any `json:"attrs,omitempty"`
}

// JITTimeline records JIT lifecycle events as either JSONL or a JSON array.
// It is safe for concurrent use; the normal VM path is single-threaded, but
// the lock keeps CLI diagnostics robust if goroutine support exercises JIT.
type JITTimeline struct {
	mu     sync.Mutex
	w      io.Writer
	format string
	enc    *json.Encoder
	seq    uint64
	events []JITTimelineEvent
	err    error
}

func NewJITTimeline(w io.Writer, format string) (*JITTimeline, error) {
	if w == nil {
		return nil, fmt.Errorf("jit timeline: nil writer")
	}
	switch format {
	case "", JITTimelineJSONL:
		format = JITTimelineJSONL
	case JITTimelineJSON:
	default:
		return nil, fmt.Errorf("jit timeline: unsupported format %q", format)
	}
	t := &JITTimeline{w: w, format: format}
	if format == JITTimelineJSONL {
		t.enc = json.NewEncoder(w)
	}
	return t, nil
}

func (t *JITTimeline) Record(ev JITTimelineEvent) {
	if t == nil {
		return
	}
	now := time.Now().UTC()

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return
	}
	t.seq++
	ev.Seq = t.seq
	ev.Time = now.Format(time.RFC3339Nano)
	ev.UnixNano = now.UnixNano()
	if t.format == JITTimelineJSONL {
		t.err = t.enc.Encode(ev)
		return
	}
	t.events = append(t.events, ev)
}

// Flush writes buffered JSON-array timelines and returns the first write error.
// JSONL timelines are emitted on Record, so Flush only reports prior errors.
func (t *JITTimeline) Flush() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.err != nil {
		return t.err
	}
	if t.format == JITTimelineJSON {
		t.err = json.NewEncoder(t.w).Encode(t.events)
	}
	return t.err
}

func (tm *TieringManager) SetTimeline(t *JITTimeline) {
	tm.timeline = t
}

func (tm *TieringManager) traceEvent(event, tier string, proto *vm.FuncProto, attrs map[string]any) {
	if tm == nil || tm.timeline == nil {
		return
	}
	ev := JITTimelineEvent{
		Event:   event,
		Tier:    tier,
		Proto:   traceProtoName(proto),
		ProtoID: traceProtoID(proto),
		Attrs:   attrs,
	}
	tm.timeline.Record(ev)
}

func (tm *TieringManager) traceTier1CompileResult(proto *vm.FuncProto, alreadyCompiled bool, compiled interface{}, reason string) {
	if tm == nil || tm.timeline == nil {
		return
	}
	callCount := 0
	if proto != nil {
		callCount = proto.CallCount
	}
	if compiled == nil {
		tm.traceEvent("tier1_skip", "tier1", proto, map[string]any{
			"reason":     reason,
			"call_count": callCount,
		})
		return
	}
	if alreadyCompiled {
		return
	}
	bf, _ := compiled.(*BaselineFunc)
	attrs := map[string]any{
		"reason":     reason,
		"call_count": callCount,
	}
	if bf != nil && bf.Code != nil {
		attrs["code_bytes"] = bf.Code.Size()
	}
	tm.traceEvent("tier1_compile", "tier1", proto, attrs)
}

func (tm *TieringManager) traceTier2Success(proto *vm.FuncProto, cf *CompiledFunction, attempt int) {
	if tm == nil || tm.timeline == nil {
		return
	}
	attrs := map[string]any{"attempt": attempt}
	if cf != nil && cf.Code != nil {
		attrs["code_bytes"] = cf.Code.Size()
		attrs["num_regs"] = cf.numRegs
		attrs["direct_entry"] = cf.DirectEntryOffset > 0
	}
	tm.traceEvent("tier2_success", "tier2", proto, attrs)
}

func traceProtoName(proto *vm.FuncProto) string {
	if proto == nil {
		return ""
	}
	if proto.Name != "" {
		return proto.Name
	}
	if proto.Source != "" {
		return proto.Source
	}
	return "<anonymous>"
}

func traceProtoID(proto *vm.FuncProto) string {
	if proto == nil {
		return ""
	}
	return fmt.Sprintf("%p", proto)
}
