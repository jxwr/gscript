//go:build darwin && arm64

package jit

import (
	"testing"
)

func TestPipelineDefault(t *testing.T) {
	p := DefaultPipeline()
	if len(p.passes) != 7 {
		t.Fatalf("DefaultPipeline: expected 7 passes, got %d", len(p.passes))
	}
	expected := []string{"while-loop-detect", "const-hoist", "cse", "strength-reduce", "dce", "load-elim", "fma"}
	for i, name := range expected {
		if p.passes[i].Name != name {
			t.Errorf("pass %d: expected name %q, got %q", i, name, p.passes[i].Name)
		}
		if !p.passes[i].Enabled {
			t.Errorf("pass %d (%s): expected enabled", i, name)
		}
	}
}

func TestPipelineDisableEnable(t *testing.T) {
	p := DefaultPipeline()

	// Disable an existing pass.
	if !p.Disable("cse") {
		t.Fatal("Disable('cse') returned false, expected true")
	}
	for _, pass := range p.passes {
		if pass.Name == "cse" && pass.Enabled {
			t.Fatal("cse pass should be disabled")
		}
	}

	// Re-enable it.
	if !p.Enable("cse") {
		t.Fatal("Enable('cse') returned false, expected true")
	}
	for _, pass := range p.passes {
		if pass.Name == "cse" && !pass.Enabled {
			t.Fatal("cse pass should be enabled")
		}
	}

	// Disable a non-existent pass.
	if p.Disable("nonexistent") {
		t.Fatal("Disable('nonexistent') returned true, expected false")
	}
	if p.Enable("nonexistent") {
		t.Fatal("Enable('nonexistent') returned true, expected false")
	}
}

func TestPipelineRunOrder(t *testing.T) {
	p := NewPipeline()

	// Track execution order using a slice captured by closures.
	var order []string

	p.Add("first", func(f *SSAFunc) *SSAFunc {
		order = append(order, "first")
		return f
	})
	p.Add("second", func(f *SSAFunc) *SSAFunc {
		order = append(order, "second")
		return f
	})
	p.Add("third", func(f *SSAFunc) *SSAFunc {
		order = append(order, "third")
		return f
	})

	f := &SSAFunc{}
	_ = p.Run(f)

	if len(order) != 3 {
		t.Fatalf("expected 3 passes executed, got %d", len(order))
	}
	for i, name := range []string{"first", "second", "third"} {
		if order[i] != name {
			t.Errorf("pass %d: expected %q, got %q", i, name, order[i])
		}
	}
}

func TestPipelineRunSkipsDisabled(t *testing.T) {
	p := NewPipeline()

	var order []string

	p.Add("a", func(f *SSAFunc) *SSAFunc {
		order = append(order, "a")
		return f
	})
	p.Add("b", func(f *SSAFunc) *SSAFunc {
		order = append(order, "b")
		return f
	})
	p.Add("c", func(f *SSAFunc) *SSAFunc {
		order = append(order, "c")
		return f
	})

	p.Disable("b")
	f := &SSAFunc{}
	_ = p.Run(f)

	if len(order) != 2 {
		t.Fatalf("expected 2 passes executed (b disabled), got %d", len(order))
	}
	if order[0] != "a" || order[1] != "c" {
		t.Errorf("expected [a, c], got %v", order)
	}
}

func TestPipelineEmpty(t *testing.T) {
	p := NewPipeline()
	f := &SSAFunc{Insts: []SSAInst{{Op: SSA_NOP}}}
	result := p.Run(f)
	if result != f {
		t.Fatal("empty pipeline should return the same SSAFunc pointer")
	}
	if len(result.Insts) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(result.Insts))
	}
}
