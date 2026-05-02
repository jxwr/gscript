package vm

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
)

func TestProducerConsumerConsumeKernelMatchesFallback(t *testing.T) {
	src := `
func make_producer(n) {
    return coroutine.create(func() {
        for i := 1; i <= n; i++ {
            kind := "view"
            if i % 13 == 0 {
                kind = "error"
            } elseif i % 5 == 0 {
                kind = "click"
            }
            event := {
                id: i,
                account: (i * 17) % 257,
                shard: (i * 7) % 16 + 1,
                kind: kind,
                value: (i * 29) % 1000
            }
            coroutine.yield(event)
        }
        return nil
    })
}

func consume(n) {
    co := make_producer(n)
    by_shard := {}
    for i := 1; i <= 16; i++ {
        by_shard[i] = {count: 0, value: 0, errors: 0}
    }

    checksum := 0
    for i := 1; i <= n; i++ {
        ok, event := coroutine.resume(co)
        if !ok {
            return checksum
        }
        agg := by_shard[event.shard]
        agg.count = agg.count + 1
        agg.value = agg.value + event.value
        if event.kind == "error" {
            agg.errors = agg.errors + 1
            checksum = (checksum + event.account * 5 + agg.errors) % 1000000007
        } else {
            checksum = (checksum + event.value + agg.count + #event.kind) % 1000000007
        }
    }

    for i := 1; i <= 16; i++ {
        agg := by_shard[i]
        checksum = (checksum + agg.count * 3 + agg.value + agg.errors * 101) % 1000000007
    }
    return checksum
}

result := consume(6500)
`
	proto := compileMixedInventoryKernelTestProgram(t, src)
	if len(proto.Protos) != 2 {
		t.Fatalf("nested protos = %d, want 2", len(proto.Protos))
	}
	requireKernelInfo(t, RecognizedWholeCallKernels(proto.Protos[1]), "producer_consumer_consume")

	globals := runtime.NewInterpreterGlobals()
	vm := New(globals)
	defer vm.Close()
	if _, err := vm.Execute(proto); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := globals["result"]
	if !got.IsInt() || got.Int() != 7877368 {
		t.Fatalf("result=%v, want 7877368", got)
	}
	if proto.Protos[1].EnteredTier2 == 0 {
		t.Fatalf("producer consumer kernel did not mark consume entered")
	}
}
