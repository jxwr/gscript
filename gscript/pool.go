package gscript

import "sync"

// Pool manages a pool of VM instances for concurrent use.
// Typical use in a game server: one VM per goroutine/request.
type Pool struct {
	mu   sync.Mutex
	idle []*VM
	init func() *VM
	max  int
}

// NewPool creates a VM pool.
// init is called to create each new VM instance.
// max is the maximum number of idle VMs to keep (0 = unlimited).
func NewPool(max int, init func() *VM) *Pool {
	return &Pool{init: init, max: max}
}

// Get acquires a VM from the pool (creates one if none available).
func (p *Pool) Get() *VM {
	p.mu.Lock()
	if len(p.idle) > 0 {
		vm := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		p.mu.Unlock()
		return vm
	}
	p.mu.Unlock()
	return p.init()
}

// Put returns a VM to the pool.
func (p *Pool) Put(vm *VM) {
	p.mu.Lock()
	if p.max == 0 || len(p.idle) < p.max {
		p.idle = append(p.idle, vm)
	}
	p.mu.Unlock()
}

// Do acquires a VM, calls fn, then returns it to the pool.
func (p *Pool) Do(fn func(*VM) error) error {
	vm := p.Get()
	defer p.Put(vm)
	return fn(vm)
}

// Size returns the number of idle VMs in the pool.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.idle)
}
