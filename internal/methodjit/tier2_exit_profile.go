//go:build darwin && arm64

package methodjit

import (
	"fmt"
	"sync"

	"github.com/gscript/gscript/internal/vm"
)

const tier2RecompileQueueMinExitCount uint64 = 2

type Tier2ExitProfileKey struct {
	Proto    *vm.FuncProto
	PC       int
	ExitCode int
	OpID     int
	Reason   string
}

type Tier2ExitProfileSite struct {
	Proto           string `json:"proto"`
	PC              int    `json:"pc"`
	ExitCode        int    `json:"exit_code"`
	ExitName        string `json:"exit_name"`
	OpID            int    `json:"op_id"`
	Reason          string `json:"reason"`
	Count           uint64 `json:"count"`
	VersionHash     string `json:"version_hash,omitempty"`
	VersionGuards   int    `json:"version_guards,omitempty"`
	QueuedRecompile bool   `json:"queued_recompile,omitempty"`
}

type Tier2ExitProfileSnapshot struct {
	Total uint64                 `json:"total"`
	Sites []Tier2ExitProfileSite `json:"sites"`
}

type tier2ExitProfileCollector struct {
	mu    sync.Mutex
	total uint64
	sites map[Tier2ExitProfileKey]*Tier2ExitProfileSite
}

func (c *tier2ExitProfileCollector) record(proto *vm.FuncProto, cf *CompiledFunction, ctx *ExecContext) (Tier2ExitProfileSite, bool) {
	if c == nil || proto == nil || cf == nil || ctx == nil {
		return Tier2ExitProfileSite{}, false
	}
	switch ctx.ExitCode {
	case ExitDeopt, ExitCallExit, ExitGlobalExit, ExitTableExit, ExitOpExit:
	default:
		return Tier2ExitProfileSite{}, false
	}
	opID := exitStatsOpID(ctx)
	pc, reason := exitStatsSiteMeta(exitStatsKey{
		proto:      proto,
		cf:         cf,
		code:       int(ctx.ExitCode),
		opID:       opID,
		fallbackOp: exitStatsFallbackOp(ctx),
	})
	key := Tier2ExitProfileKey{
		Proto:    proto,
		PC:       pc,
		ExitCode: int(ctx.ExitCode),
		OpID:     opID,
		Reason:   reason,
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sites == nil {
		c.sites = make(map[Tier2ExitProfileKey]*Tier2ExitProfileSite)
	}
	c.total++
	site := c.sites[key]
	if site == nil {
		site = &Tier2ExitProfileSite{
			Proto:         exitStatsProtoName(proto),
			PC:            pc,
			ExitCode:      key.ExitCode,
			ExitName:      exitCodeName(key.ExitCode),
			OpID:          opID,
			Reason:        reason,
			VersionHash:   fmt.Sprintf("%x", cf.SpecializationVersion.Hash),
			VersionGuards: cf.SpecializationVersion.GuardCount,
		}
		c.sites[key] = site
	}
	site.Count++
	return *site, true
}

func (c *tier2ExitProfileCollector) markQueued(proto *vm.FuncProto) {
	if c == nil || proto == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, site := range c.sites {
		if key.Proto == proto {
			site.QueuedRecompile = true
		}
	}
}

func (c *tier2ExitProfileCollector) snapshot() Tier2ExitProfileSnapshot {
	if c == nil {
		return Tier2ExitProfileSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := Tier2ExitProfileSnapshot{
		Total: c.total,
		Sites: make([]Tier2ExitProfileSite, 0, len(c.sites)),
	}
	for _, site := range c.sites {
		out.Sites = append(out.Sites, *site)
	}
	return out
}

type tier2RecompileRequest struct {
	Reason string
	Site   Tier2ExitProfileSite
}

type tier2RecompileQueue struct {
	mu       sync.Mutex
	requests map[*vm.FuncProto]tier2RecompileRequest
}

func (q *tier2RecompileQueue) enqueue(proto *vm.FuncProto, reason string, site Tier2ExitProfileSite) bool {
	if q == nil || proto == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.requests == nil {
		q.requests = make(map[*vm.FuncProto]tier2RecompileRequest)
	}
	if _, exists := q.requests[proto]; exists {
		return false
	}
	q.requests[proto] = tier2RecompileRequest{Reason: reason, Site: site}
	return true
}

func (q *tier2RecompileQueue) take(proto *vm.FuncProto) (tier2RecompileRequest, bool) {
	if q == nil || proto == nil {
		return tier2RecompileRequest{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	req, ok := q.requests[proto]
	if ok {
		delete(q.requests, proto)
	}
	return req, ok
}
