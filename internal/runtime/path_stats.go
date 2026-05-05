package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
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

	structuralKernelHit sync.Map

	// nativeCallByBuiltin attributes native_call fast/fallback events to a
	// specific *GoFunction. Keys are *GoFunction; values are
	// *nativeCallBuiltinCounters. We key by pointer to avoid string hashing on
	// the hot fast path; resolution to the human-readable Name happens lazily
	// at snapshot time.
	nativeCallByBuiltin sync.Map
}

// nativeCallBuiltinCounters holds per-GoFunction fast/fallback tallies.
type nativeCallBuiltinCounters struct {
	fast     atomic.Uint64
	fallback atomic.Uint64
}

type RuntimePathStatsSnapshot struct {
	NativeCall       RuntimePathNativeCallStats       `json:"native_call"`
	Coroutine        RuntimePathCoroutineStats        `json:"coroutine"`
	TableArray       RuntimePathTableArrayStats       `json:"table_array"`
	StringFormat     RuntimePathStringStats           `json:"string_format"`
	StructuralKernel RuntimePathStructuralKernelStats `json:"structural_kernel"`
}

type RuntimePathNativeCallStats struct {
	Fast       uint64                              `json:"fast"`
	Fallback   uint64                              `json:"fallback"`
	PerBuiltin []RuntimePathNativeCallBuiltinEntry `json:"per_builtin,omitempty"`
}

// RuntimePathNativeCallBuiltinEntry is a per-GoFunction attribution row,
// sorted by fallback desc, fast desc, name asc at snapshot time.
type RuntimePathNativeCallBuiltinEntry struct {
	Name     string `json:"name"`
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

type RuntimePathStructuralKernelStats struct {
	Total     uint64                             `json:"total"`
	PerKernel []RuntimePathStructuralKernelEntry `json:"per_kernel,omitempty"`
}

// RuntimePathStructuralKernelEntry attributes guarded structural-kernel hits.
// Route is a stable VM-level category such as whole_call_value or
// whole_call_no_result; Name is the structural recognizer name.
type RuntimePathStructuralKernelEntry struct {
	Route string `json:"route"`
	Name  string `json:"name"`
	Count uint64 `json:"count"`
}

type structuralKernelStatsKey struct {
	route string
	name  string
}

type structuralKernelCounters struct {
	count atomic.Uint64
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
			Fast:       s.nativeCallFast.Load(),
			Fallback:   s.nativeCallFallback.Load(),
			PerBuiltin: s.snapshotNativeCallPerBuiltin(),
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
		StructuralKernel: s.snapshotStructuralKernels(),
	}
}

func (s *RuntimePathStats) WriteText(w io.Writer) {
	snap := s.Snapshot()
	fmt.Fprintln(w, "Runtime Path Statistics:")
	fmt.Fprintln(w, "  native_call:")
	fmt.Fprintf(w, "    fast: %d\n", snap.NativeCall.Fast)
	fmt.Fprintf(w, "    fallback: %d\n", snap.NativeCall.Fallback)
	if len(snap.NativeCall.PerBuiltin) > 0 {
		fmt.Fprintln(w, "    per_builtin:")
		for _, e := range snap.NativeCall.PerBuiltin {
			fmt.Fprintf(w, "      %s: fast=%d fallback=%d\n", e.Name, e.Fast, e.Fallback)
		}
	}
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
	fmt.Fprintln(w, "  structural_kernel:")
	fmt.Fprintf(w, "    total: %d\n", snap.StructuralKernel.Total)
	if len(snap.StructuralKernel.PerKernel) > 0 {
		fmt.Fprintln(w, "    per_kernel:")
		for _, e := range snap.StructuralKernel.PerKernel {
			fmt.Fprintf(w, "      %s/%s: count=%d\n", e.Route, e.Name, e.Count)
		}
	}
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

// RecordRuntimePathNativeCallFastFor attributes a fast-path native call to a
// specific *GoFunction. It is identical in cost to
// RecordRuntimePathNativeCallFast when stats are disabled (single atomic load
// + nil check); when enabled it additionally bumps the per-builtin counter.
func RecordRuntimePathNativeCallFastFor(gf *GoFunction) {
	s := runtimePathStats.Load()
	if s == nil {
		return
	}
	s.nativeCallFast.Add(1)
	if gf == nil {
		return
	}
	c := s.loadOrCreateBuiltin(gf)
	c.fast.Add(1)
}

// RecordRuntimePathNativeCallFallbackFor attributes a fallback-path native
// call to a specific *GoFunction. Same enabled/disabled cost shape as the fast
// variant.
func RecordRuntimePathNativeCallFallbackFor(gf *GoFunction) {
	s := runtimePathStats.Load()
	if s == nil {
		return
	}
	s.nativeCallFallback.Add(1)
	if gf == nil {
		return
	}
	c := s.loadOrCreateBuiltin(gf)
	c.fallback.Add(1)
}

func (s *RuntimePathStats) loadOrCreateBuiltin(gf *GoFunction) *nativeCallBuiltinCounters {
	if v, ok := s.nativeCallByBuiltin.Load(gf); ok {
		return v.(*nativeCallBuiltinCounters)
	}
	c := &nativeCallBuiltinCounters{}
	if actual, loaded := s.nativeCallByBuiltin.LoadOrStore(gf, c); loaded {
		return actual.(*nativeCallBuiltinCounters)
	}
	return c
}

func (s *RuntimePathStats) snapshotNativeCallPerBuiltin() []RuntimePathNativeCallBuiltinEntry {
	var out []RuntimePathNativeCallBuiltinEntry
	s.nativeCallByBuiltin.Range(func(k, v any) bool {
		gf, _ := k.(*GoFunction)
		c, _ := v.(*nativeCallBuiltinCounters)
		if gf == nil || c == nil {
			return true
		}
		name := gf.Name
		if name == "" {
			name = fmt.Sprintf("<unnamed:%p>", gf)
		}
		out = append(out, RuntimePathNativeCallBuiltinEntry{
			Name:     name,
			Fast:     c.fast.Load(),
			Fallback: c.fallback.Load(),
		})
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Fallback != out[j].Fallback {
			return out[i].Fallback > out[j].Fallback
		}
		if out[i].Fast != out[j].Fast {
			return out[i].Fast > out[j].Fast
		}
		return out[i].Name < out[j].Name
	})
	return out
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

// RecordRuntimePathStructuralKernelHit attributes a guarded structural-kernel
// execution. It is diagnostic-only; disabled runs pay one atomic pointer load
// and a nil check.
func RecordRuntimePathStructuralKernelHit(route, name string) {
	s := runtimePathStats.Load()
	if s == nil || route == "" || name == "" {
		return
	}
	c := s.loadOrCreateStructuralKernel(route, name)
	c.count.Add(1)
}

func (s *RuntimePathStats) loadOrCreateStructuralKernel(route, name string) *structuralKernelCounters {
	key := structuralKernelStatsKey{route: route, name: name}
	if v, ok := s.structuralKernelHit.Load(key); ok {
		return v.(*structuralKernelCounters)
	}
	c := &structuralKernelCounters{}
	if actual, loaded := s.structuralKernelHit.LoadOrStore(key, c); loaded {
		return actual.(*structuralKernelCounters)
	}
	return c
}

func (s *RuntimePathStats) snapshotStructuralKernels() RuntimePathStructuralKernelStats {
	var out []RuntimePathStructuralKernelEntry
	var total uint64
	s.structuralKernelHit.Range(func(k, v any) bool {
		key, _ := k.(structuralKernelStatsKey)
		c, _ := v.(*structuralKernelCounters)
		if c == nil {
			return true
		}
		count := c.count.Load()
		total += count
		out = append(out, RuntimePathStructuralKernelEntry{
			Route: key.route,
			Name:  key.name,
			Count: count,
		})
		return true
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Route != out[j].Route {
			return out[i].Route < out[j].Route
		}
		return out[i].Name < out[j].Name
	})
	return RuntimePathStructuralKernelStats{Total: total, PerKernel: out}
}
