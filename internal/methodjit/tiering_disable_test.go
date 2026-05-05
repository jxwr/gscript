//go:build darwin && arm64

package methodjit

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestDisableJITForTier0PolicyTraceFields(t *testing.T) {
	var buf bytes.Buffer
	timeline, err := NewJITTimeline(&buf, JITTimelineJSON)
	if err != nil {
		t.Fatalf("NewJITTimeline: %v", err)
	}
	tm := NewTieringManager()
	tm.SetTimeline(timeline)

	proto := &vm.FuncProto{Name: "driver", CallCount: BaselineCompileThreshold}
	callee := &vm.FuncProto{Name: "leaf"}
	tm.disableJITForTier0Policy(proto, tier0DisableDecision{
		reason:         "tier1_driver_tier0_loop_callee",
		fallbackReason: "driver_tier0_loop_callee",
		callee:         callee,
	})
	if !proto.JITDisabled {
		t.Fatal("proto was not marked JITDisabled")
	}
	if err := timeline.Flush(); err != nil {
		t.Fatalf("timeline flush: %v", err)
	}

	var events []JITTimelineEvent
	if err := json.Unmarshal(buf.Bytes(), &events); err != nil {
		t.Fatalf("unmarshal timeline: %v\n%s", err, buf.String())
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3: %+v", len(events), events)
	}
	if events[0].Event != "runtime_disable" || events[0].Attrs["callee"] != "leaf" || events[0].Attrs["callee_addr"] == "" {
		t.Fatalf("runtime_disable attrs = %+v", events[0])
	}
	if events[1].Event != "tier1_skip" || events[1].Attrs["callee"] != "leaf" {
		t.Fatalf("tier1_skip attrs = %+v", events[1])
	}
	if events[2].Event != "fallback" || events[2].Attrs["reason"] != "driver_tier0_loop_callee" || events[2].Attrs["target"] != "interpreter" {
		t.Fatalf("fallback attrs = %+v", events[2])
	}
}
