//go:build darwin && arm64

package methodjit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJITTimelineJSONL(t *testing.T) {
	var buf bytes.Buffer
	timeline, err := NewJITTimeline(&buf, JITTimelineJSONL)
	if err != nil {
		t.Fatalf("NewJITTimeline: %v", err)
	}

	timeline.Record(JITTimelineEvent{
		Event: "tier2_attempt",
		Tier:  "tier2",
		Proto: "hot",
		Attrs: map[string]any{"attempt": 1},
	})
	if err := timeline.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), buf.String())
	}
	var ev JITTimelineEvent
	if err := json.Unmarshal([]byte(lines[0]), &ev); err != nil {
		t.Fatalf("unmarshal JSONL event: %v", err)
	}
	if ev.Seq != 1 || ev.Event != "tier2_attempt" || ev.Tier != "tier2" || ev.Proto != "hot" {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Time == "" || ev.UnixNano == 0 {
		t.Fatalf("timestamp fields not populated: %+v", ev)
	}
}

func TestJITTimelineJSON(t *testing.T) {
	var buf bytes.Buffer
	timeline, err := NewJITTimeline(&buf, JITTimelineJSON)
	if err != nil {
		t.Fatalf("NewJITTimeline: %v", err)
	}

	timeline.Record(JITTimelineEvent{Event: "tier1_compile", Tier: "tier1", Proto: "a"})
	timeline.Record(JITTimelineEvent{Event: "fallback", Tier: "tier0", Proto: "b", Attrs: map[string]any{"target": "interpreter"}})
	if err := timeline.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var events []JITTimelineEvent
	if err := json.Unmarshal(buf.Bytes(), &events); err != nil {
		t.Fatalf("unmarshal JSON events: %v\n%s", err, buf.String())
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("sequence not monotonic: %+v", events)
	}
	if events[1].Attrs["target"] != "interpreter" {
		t.Fatalf("missing attrs: %+v", events[1].Attrs)
	}
}

func TestNewJITTimelineRejectsUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if _, err := NewJITTimeline(&buf, "yaml"); err == nil {
		t.Fatal("expected unknown format error")
	}
}
