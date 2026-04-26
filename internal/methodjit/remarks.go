package methodjit

import (
	"fmt"
	"strings"
)

// OptimizationRemark is one structured note produced by the Tier 2 pipeline.
// Remarks are diagnostics, not a compilation control path: production compiles
// pass nil and pay no allocation cost.
type OptimizationRemark struct {
	Pass    string `json:"pass"`
	Kind    string `json:"kind"`
	BlockID int    `json:"block_id"`
	ValueID int    `json:"value_id"`
	Op      string `json:"op,omitempty"`
	Reason  string `json:"reason"`
}

// OptimizationRemarks collects a bounded, de-duplicated remark stream.
type OptimizationRemarks struct {
	items []OptimizationRemark
	seen  map[string]bool
}

const maxOptimizationRemarks = 256

func (r *OptimizationRemarks) Add(pass, kind string, blockID, valueID int, op Op, reason string) {
	if r == nil || reason == "" || len(r.items) >= maxOptimizationRemarks {
		return
	}
	remark := OptimizationRemark{
		Pass:    pass,
		Kind:    kind,
		BlockID: blockID,
		ValueID: valueID,
		Reason:  reason,
	}
	if op != OpNop {
		remark.Op = op.String()
	}
	key := fmt.Sprintf("%s|%s|%d|%d|%s|%s", remark.Pass, remark.Kind, remark.BlockID, remark.ValueID, remark.Op, remark.Reason)
	if r.seen == nil {
		r.seen = make(map[string]bool)
	}
	if r.seen[key] {
		return
	}
	r.seen[key] = true
	r.items = append(r.items, remark)
}

func (r *OptimizationRemarks) List() []OptimizationRemark {
	if r == nil || len(r.items) == 0 {
		return nil
	}
	out := make([]OptimizationRemark, len(r.items))
	copy(out, r.items)
	return out
}

func formatOptimizationRemarks(remarks []OptimizationRemark) string {
	if len(remarks) == 0 {
		return "(none)\n"
	}
	var b strings.Builder
	for _, remark := range remarks {
		loc := ""
		if remark.BlockID > 0 || remark.ValueID > 0 {
			loc = fmt.Sprintf(" B%d/v%d", remark.BlockID, remark.ValueID)
		}
		op := ""
		if remark.Op != "" {
			op = " " + remark.Op
		}
		fmt.Fprintf(&b, "  [%s] %s%s%s: %s\n", remark.Kind, remark.Pass, loc, op, remark.Reason)
	}
	return b.String()
}

func functionRemarks(fn *Function) *OptimizationRemarks {
	if fn == nil {
		return nil
	}
	return fn.Remarks
}
