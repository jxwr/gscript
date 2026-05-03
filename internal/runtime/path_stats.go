package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
)

// RuntimePathStats contains optional runtime path counters. The global pointer
// is nil by default, so disabled runs pay only the load/branch at instrumented
// diagnostic points.
type RuntimePathStats struct {
	nativeCallFast     atomic.Uint64
	nativeCallFallback atomic.Uint64

	coroutineResume       atomic.Uint64
	coroutineYield        atomic.Uint64
	coroutineFast         atomic.Uint64
	coroutineFallback     atomic.Uint64
	coroutineResumeErrors atomic.Uint64

	tableArrayGetHot      atomic.Uint64
	tableArrayGetFallback atomic.Uint64
	tableArraySetHot      atomic.Uint64
	tableArraySetFallback atomic.Uint64

	stringFormatFast     atomic.Uint64
	stringFormatFallback atomic.Uint64
}

type RuntimePathStatsSnapshot struct {
	NativeCall   RuntimePathNativeCallStats `json:"native_call"`
	Coroutine    RuntimePathCoroutineStats  `json:"coroutine"`
	TableArray   RuntimePathTableArrayStats `json:"table_array"`
	StringFormat RuntimePathStringStats     `json:"string_format"`
}

type RuntimePathNativeCallStats struct {
	Fast     uint64 `json:"fast"`
	Fallback uint64 `json:"fallback"`
}

type RuntimePathCoroutineStats struct {
	Resume       uint64 `json:"resume"`
	Yield        uint64 `json:"yield"`
	Fast         uint64 `json:"fast"`
	Fallback     uint64 `json:"fallback"`
	ResumeErrors uint64 `json:"resume_errors"`
}

type RuntimePathTableArrayStats struct {
	GetHot      uint64 `json:"get_hot"`
	GetFallback uint64 `json:"get_fallback"`
	SetHot      uint64 `json:"set_hot"`
	SetFallback uint64 `json:"set_fallback"`
}

type RuntimePathStringStats struct {
	Fast     uint64 `json:"fast"`
	Fallback uint64 `json:"fallback"`
}

var runtimePathStats atomic.Pointer[RuntimePathStats]

func EnableRuntimePathStats() *RuntimePathStats {
	stats := &RuntimePathStats{}
	runtimePathStats.Store(stats)
	return stats
}

func DisableRuntimePathStats() {
	runtimePathStats.Store(nil)
}

func CurrentRuntimePathStats() *RuntimePathStats {
	return runtimePathStats.Load()
}

func (s *RuntimePathStats) Snapshot() RuntimePathStatsSnapshot {
	if s == nil {
		return RuntimePathStatsSnapshot{}
	}
	return RuntimePathStatsSnapshot{
		NativeCall: RuntimePathNativeCallStats{
			Fast:     s.nativeCallFast.Load(),
			Fallback: s.nativeCallFallback.Load(),
		},
		Coroutine: RuntimePathCoroutineStats{
			Resume:       s.coroutineResume.Load(),
			Yield:        s.coroutineYield.Load(),
			Fast:         s.coroutineFast.Load(),
			Fallback:     s.coroutineFallback.Load(),
			ResumeErrors: s.coroutineResumeErrors.Load(),
		},
		TableArray: RuntimePathTableArrayStats{
			GetHot:      s.tableArrayGetHot.Load(),
			GetFallback: s.tableArrayGetFallback.Load(),
			SetHot:      s.tableArraySetHot.Load(),
			SetFallback: s.tableArraySetFallback.Load(),
		},
		StringFormat: RuntimePathStringStats{
			Fast:     s.stringFormatFast.Load(),
			Fallback: s.stringFormatFallback.Load(),
		},
	}
}

func (s *RuntimePathStats) WriteText(w io.Writer) {
	snap := s.Snapshot()
	fmt.Fprintln(w, "Runtime Path Statistics:")
	fmt.Fprintln(w, "  native_call:")
	fmt.Fprintf(w, "    fast: %d\n", snap.NativeCall.Fast)
	fmt.Fprintf(w, "    fallback: %d\n", snap.NativeCall.Fallback)
	fmt.Fprintln(w, "  coroutine:")
	fmt.Fprintf(w, "    resume: %d\n", snap.Coroutine.Resume)
	fmt.Fprintf(w, "    yield: %d\n", snap.Coroutine.Yield)
	fmt.Fprintf(w, "    fast: %d\n", snap.Coroutine.Fast)
	fmt.Fprintf(w, "    fallback: %d\n", snap.Coroutine.Fallback)
	fmt.Fprintf(w, "    resume_errors: %d\n", snap.Coroutine.ResumeErrors)
	fmt.Fprintln(w, "  table_array:")
	fmt.Fprintf(w, "    get_hot: %d\n", snap.TableArray.GetHot)
	fmt.Fprintf(w, "    get_fallback: %d\n", snap.TableArray.GetFallback)
	fmt.Fprintf(w, "    set_hot: %d\n", snap.TableArray.SetHot)
	fmt.Fprintf(w, "    set_fallback: %d\n", snap.TableArray.SetFallback)
	fmt.Fprintln(w, "  string_format:")
	fmt.Fprintf(w, "    fast: %d\n", snap.StringFormat.Fast)
	fmt.Fprintf(w, "    fallback: %d\n", snap.StringFormat.Fallback)
}

func (s *RuntimePathStats) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s.Snapshot())
}

func RecordRuntimePathNativeCallFast() {
	if s := runtimePathStats.Load(); s != nil {
		s.nativeCallFast.Add(1)
	}
}

func RecordRuntimePathNativeCallFallback() {
	if s := runtimePathStats.Load(); s != nil {
		s.nativeCallFallback.Add(1)
	}
}

func RecordRuntimePathCoroutineResume() {
	if s := runtimePathStats.Load(); s != nil {
		s.coroutineResume.Add(1)
	}
}

func RecordRuntimePathCoroutineYield() {
	if s := runtimePathStats.Load(); s != nil {
		s.coroutineYield.Add(1)
	}
}

func RecordRuntimePathCoroutineFast() {
	if s := runtimePathStats.Load(); s != nil {
		s.coroutineFast.Add(1)
	}
}

func RecordRuntimePathCoroutineFallback() {
	if s := runtimePathStats.Load(); s != nil {
		s.coroutineFallback.Add(1)
	}
}

func RecordRuntimePathCoroutineResumeError() {
	if s := runtimePathStats.Load(); s != nil {
		s.coroutineResumeErrors.Add(1)
	}
}

func RecordRuntimePathTableArrayGetHot() {
	if s := runtimePathStats.Load(); s != nil {
		s.tableArrayGetHot.Add(1)
	}
}

func RecordRuntimePathTableArrayGetFallback() {
	if s := runtimePathStats.Load(); s != nil {
		s.tableArrayGetFallback.Add(1)
	}
}

func RecordRuntimePathTableArraySetHot() {
	if s := runtimePathStats.Load(); s != nil {
		s.tableArraySetHot.Add(1)
	}
}

func RecordRuntimePathTableArraySetFallback() {
	if s := runtimePathStats.Load(); s != nil {
		s.tableArraySetFallback.Add(1)
	}
}

func RecordRuntimePathStringFormatFast() {
	if s := runtimePathStats.Load(); s != nil {
		s.stringFormatFast.Add(1)
	}
}

func RecordRuntimePathStringFormatFallback() {
	if s := runtimePathStats.Load(); s != nil {
		s.stringFormatFallback.Add(1)
	}
}
