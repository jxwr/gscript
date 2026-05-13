//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/vm"
)

func TestTier2NeedsNativeStackReserveSkipsLeafTypedPeer(t *testing.T) {
	cf := &CompiledFunction{
		Proto:        &vm.FuncProto{Tier2LeafNoCall: true},
		TypedPeerABI: TypedSelfABI{Eligible: true},
	}
	if tier2NeedsNativeStackReserve(cf) {
		t.Fatal("leaf typed-peer entry should not reserve native stack")
	}
	cf.Proto.Tier2LeafNoCall = false
	if !tier2NeedsNativeStackReserve(cf) {
		t.Fatal("non-leaf typed-peer entry should reserve native stack")
	}
	cf.TypedPeerABI = TypedSelfABI{}
	cf.RawIntSelfABI = RawIntSelfABI{Eligible: true}
	if !tier2NeedsNativeStackReserve(cf) {
		t.Fatal("raw-int self ABI still needs native stack reserve")
	}
}
