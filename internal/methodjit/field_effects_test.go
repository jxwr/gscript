package methodjit

import "testing"

func TestSummarizeFieldEffects_RecordsStaticParamFieldWrites(t *testing.T) {
	src := `func f(a, tick) {
		a.x = a.x + tick
		a.state = "read"
		return a.x
	}`
	proto := compileFunction(t, src)
	effects := SummarizeFieldEffects(proto)
	if !effects.ParamMutationKnown(0) {
		t.Fatalf("param mutation should be known: %s", effects.FormatParam(0))
	}
	if !effects.WritesParamField(0, "x") || !effects.WritesParamField(0, "state") {
		t.Fatalf("missing field writes: %s", effects.FormatParam(0))
	}
	if effects.WritesParamField(0, "kind") {
		t.Fatalf("unexpected kind write: %s", effects.FormatParam(0))
	}
}

func TestSummarizeFieldEffects_MarksDynamicParamMutationUnknown(t *testing.T) {
	src := `func f(a, k, v) {
		a[k] = v
		return v
	}`
	proto := compileFunction(t, src)
	effects := SummarizeFieldEffects(proto)
	if effects.ParamMutationKnown(0) {
		t.Fatalf("dynamic table write should be unknown: %s", effects.FormatParam(0))
	}
}
