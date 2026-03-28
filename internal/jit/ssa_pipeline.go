//go:build darwin && arm64

package jit

// Pass represents a single optimization pass in the compilation pipeline.
type Pass struct {
	Name    string
	Fn      func(f *SSAFunc) *SSAFunc
	Enabled bool
}

// Pipeline manages the ordered list of optimization passes.
type Pipeline struct {
	passes []Pass
}

// NewPipeline creates a new empty pipeline.
func NewPipeline() *Pipeline {
	return &Pipeline{}
}

// Add registers a new pass at the end of the pipeline.
func (p *Pipeline) Add(name string, fn func(f *SSAFunc) *SSAFunc) {
	p.passes = append(p.passes, Pass{Name: name, Fn: fn, Enabled: true})
}

// Disable disables a pass by name. Returns true if the pass was found.
func (p *Pipeline) Disable(name string) bool {
	for i := range p.passes {
		if p.passes[i].Name == name {
			p.passes[i].Enabled = false
			return true
		}
	}
	return false
}

// Enable enables a pass by name. Returns true if the pass was found.
func (p *Pipeline) Enable(name string) bool {
	for i := range p.passes {
		if p.passes[i].Name == name {
			p.passes[i].Enabled = true
			return true
		}
	}
	return false
}

// Run executes all enabled passes in order on the SSA function.
func (p *Pipeline) Run(f *SSAFunc) *SSAFunc {
	for _, pass := range p.passes {
		if pass.Enabled {
			f = pass.Fn(f)
		}
	}
	return f
}

// DefaultPipeline returns the standard compilation pipeline with all current passes.
func DefaultPipeline() *Pipeline {
	p := NewPipeline()
	p.Add("while-loop-detect", OptimizeSSA)
	p.Add("const-hoist", ConstHoist)
	p.Add("cse", CSE)
	p.Add("strength-reduce", StrengthReduce)
	p.Add("dce", DCE)
	p.Add("load-elim", LoadElimination)
	p.Add("fma", FuseMultiplyAdd)
	return p
}
