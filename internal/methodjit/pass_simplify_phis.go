// pass_simplify_phis.go implements Braun et al. 2013 Algorithm 5 —
// "Remove redundant phi SCCs." It is a global post-construction cleanup
// over *Function that collapses strongly connected components of phi
// instructions whose only outside-operand set is a single value.
//
// Motivation: the graph builder's per-phi tryRemoveTrivialPhi collapses
// each phi in isolation. It cannot see that a group of phis mutually
// reference each other with only one outside operand — e.g. a nested
// loop header whose inner loop-invariant phis reference each other
// across the back-edge. Those phis form an SCC in the phi-subgraph
// with exactly one non-phi outer operand; Algorithm 5 is the minimum
// cleanup that collapses them.
//
// The pass:
//  1. Collects all phi instructions and builds the phi-subgraph:
//     edges phi -> arg.Def iff arg.Def is also a phi.
//  2. Runs Tarjan SCC (reverse-topological order: children first).
//  3. For each SCC, computes the set of "outer" value IDs — args whose
//     Def is NOT a phi in this SCC (honouring replacements already
//     recorded for previously-processed SCCs).
//  4. If the outer set has exactly one element, records each phi in the
//     SCC as replaced by that outer value and removes it from its block.
//  5. Finally, rewrites every instruction's Args through the replacement
//     map (with path compression).
//
// The pass is a no-op on functions with no phis. It never mutates the
// CFG, never touches regalloc bookkeeping, and leaves builder state
// (block.defs / block.incomplete) alone — they are already obsolete
// by the time the Tier 2 pipeline runs.

package methodjit

// SimplifyPhisPass removes redundant phi SCCs. See file comment for
// the algorithm. Idempotent; safe to run multiple times.
func SimplifyPhisPass(fn *Function) (*Function, error) {
	if fn == nil {
		return nil, nil
	}

	// Collect all phis.
	phis := make([]*Instr, 0, 16)
	phiSet := make(map[int]*Instr)
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			if instr.Op == OpPhi {
				phis = append(phis, instr)
				phiSet[instr.ID] = instr
			}
		}
	}
	if len(phis) == 0 {
		return fn, nil
	}

	// Replacement map: phi ID -> value it has been replaced by.
	// Values may point at other replaced IDs; resolve with path compression.
	replacement := make(map[int]*Value)

	// resolve walks the replacement chain to find the ultimate rewrite
	// target for a value ID. Returns nil if the ID has no replacement.
	var resolve func(*Value) *Value
	resolve = func(v *Value) *Value {
		if v == nil {
			return nil
		}
		cur := v
		for {
			next, ok := replacement[cur.ID]
			if !ok {
				return cur
			}
			if next == nil || next.ID == cur.ID {
				return cur
			}
			cur = next
		}
	}

	// Tarjan SCC on the phi-subgraph. Nodes are phi IDs, edges are
	// phi -> arg.Def where arg.Def is another phi in phiSet.
	var (
		index    = 0
		indices  = make(map[int]int)
		lowlink  = make(map[int]int)
		onStack  = make(map[int]bool)
		stack    = make([]*Instr, 0, len(phis))
		sccs     = make([][]*Instr, 0) // reverse-topological
	)

	var strongconnect func(*Instr)
	strongconnect = func(p *Instr) {
		indices[p.ID] = index
		lowlink[p.ID] = index
		index++
		stack = append(stack, p)
		onStack[p.ID] = true

		for _, arg := range p.Args {
			if arg == nil || arg.Def == nil {
				continue
			}
			q, ok := phiSet[arg.Def.ID]
			if !ok {
				continue
			}
			if _, visited := indices[q.ID]; !visited {
				strongconnect(q)
				if lowlink[q.ID] < lowlink[p.ID] {
					lowlink[p.ID] = lowlink[q.ID]
				}
			} else if onStack[q.ID] {
				if indices[q.ID] < lowlink[p.ID] {
					lowlink[p.ID] = indices[q.ID]
				}
			}
		}

		if lowlink[p.ID] == indices[p.ID] {
			// Pop an SCC.
			var scc []*Instr
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top.ID] = false
				scc = append(scc, top)
				if top.ID == p.ID {
					break
				}
			}
			sccs = append(sccs, scc)
		}
	}

	for _, p := range phis {
		if _, visited := indices[p.ID]; !visited {
			strongconnect(p)
		}
	}

	// Process SCCs in reverse-topological order (which is the order
	// Tarjan produces): children first. For each SCC, compute the set
	// of outer operand values (values not belonging to this SCC, after
	// honouring replacements already recorded).
	removed := make(map[int]bool)

	for _, scc := range sccs {
		// Build a set of IDs belonging to this SCC.
		inSCC := make(map[int]bool, len(scc))
		for _, p := range scc {
			inSCC[p.ID] = true
		}

		// Collect outer operands (deduped by value ID).
		outer := make(map[int]*Value)
		for _, p := range scc {
			for _, arg := range p.Args {
				if arg == nil {
					continue
				}
				// Follow replacement chain first.
				rv := resolve(arg)
				if rv == nil {
					continue
				}
				// Is the resolved value itself one of the phis in this SCC?
				if rv.Def != nil && inSCC[rv.Def.ID] {
					continue
				}
				outer[rv.ID] = rv
			}
		}

		if len(outer) != 1 {
			continue // not collapsible
		}
		var same *Value
		for _, v := range outer {
			same = v
		}
		// Record replacements and mark phis for removal.
		for _, p := range scc {
			replacement[p.ID] = same
			removed[p.ID] = true
		}
	}

	if len(removed) == 0 {
		return fn, nil
	}

	// Remove collapsed phis from their blocks.
	for _, blk := range fn.Blocks {
		if len(blk.Instrs) == 0 {
			continue
		}
		alive := blk.Instrs[:0]
		for _, instr := range blk.Instrs {
			if instr.Op == OpPhi && removed[instr.ID] {
				continue
			}
			alive = append(alive, instr)
		}
		blk.Instrs = alive
	}

	// Rewrite all Args through the replacement map. Resolve fully so a
	// chain A -> B -> C ends up pointing directly at C.
	for _, blk := range fn.Blocks {
		for _, instr := range blk.Instrs {
			for i, arg := range instr.Args {
				if arg == nil {
					continue
				}
				if _, ok := replacement[arg.ID]; !ok {
					continue
				}
				rv := resolve(arg)
				if rv != nil && rv.ID != arg.ID {
					instr.Args[i] = rv
				}
			}
		}
	}

	return fn, nil
}
