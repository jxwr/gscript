//go:build darwin && arm64

package methodjit

import (
	"testing"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func TestBuildProtoInlineGlobalsEntryDeclarations(t *testing.T) {
	ack := &vm.FuncProto{Name: "ack"}
	top := &vm.FuncProto{
		Name:      "<main>",
		Constants: []runtime.Value{runtime.StringValue("ack")},
		Protos:    []*vm.FuncProto{ack},
		Code: []uint32{
			vm.EncodeABx(vm.OP_CLOSURE, 0, 0),
			vm.EncodeABx(vm.OP_SETGLOBAL, 0, 0),
		},
	}

	globals := buildProtoInlineGlobals(top)
	if globals["ack"] != ack {
		t.Fatalf("expected lexical global ack -> ack proto, got %#v", globals["ack"])
	}
}

func TestBuildProtoInlineGlobalsFollowsMove(t *testing.T) {
	helper := &vm.FuncProto{Name: "helper"}
	top := &vm.FuncProto{
		Name:      "<main>",
		Constants: []runtime.Value{runtime.StringValue("helper")},
		Protos:    []*vm.FuncProto{helper},
		Code: []uint32{
			vm.EncodeABx(vm.OP_CLOSURE, 1, 0),
			vm.EncodeABC(vm.OP_MOVE, 2, 1, 0),
			vm.EncodeABx(vm.OP_SETGLOBAL, 2, 0),
		},
	}

	globals := buildProtoInlineGlobals(top)
	if globals["helper"] != helper {
		t.Fatalf("expected lexical global helper -> helper proto, got %#v", globals["helper"])
	}
}

func TestBuildProtoInlineGlobalsStopsAtExecutableBody(t *testing.T) {
	early := &vm.FuncProto{Name: "early"}
	late := &vm.FuncProto{Name: "late"}
	top := &vm.FuncProto{
		Name: "<main>",
		Constants: []runtime.Value{
			runtime.StringValue("early"),
			runtime.StringValue("late"),
		},
		Protos: []*vm.FuncProto{early, late},
		Code: []uint32{
			vm.EncodeABx(vm.OP_CLOSURE, 0, 0),
			vm.EncodeABx(vm.OP_SETGLOBAL, 0, 0),
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 1),
			vm.EncodeABx(vm.OP_CLOSURE, 0, 1),
			vm.EncodeABx(vm.OP_SETGLOBAL, 0, 1),
		},
	}

	globals := buildProtoInlineGlobals(top)
	if globals["early"] != early {
		t.Fatalf("expected early declaration to be discovered, got %#v", globals["early"])
	}
	if _, ok := globals["late"]; ok {
		t.Fatalf("did not expect declaration after executable body to be discovered")
	}
}

func TestBuildProtoStableGlobalsFindsPostSetupDeclaration(t *testing.T) {
	helper := &vm.FuncProto{Name: "helper"}
	top := &vm.FuncProto{
		Name:      "<main>",
		Constants: []runtime.Value{runtime.StringValue("helper")},
		Protos:    []*vm.FuncProto{helper},
		Code: []uint32{
			vm.EncodeAsBx(vm.OP_LOADINT, 3, 1),
			vm.EncodeABx(vm.OP_CLOSURE, 1, 0),
			vm.EncodeABx(vm.OP_SETGLOBAL, 1, 0),
		},
	}

	globals := buildProtoStableGlobals(top)
	if globals["helper"] != helper {
		t.Fatalf("expected stable helper declaration after setup, got %#v", globals["helper"])
	}
}

func TestBuildProtoStableGlobalsRejectsReassignment(t *testing.T) {
	helper := &vm.FuncProto{Name: "helper"}
	top := &vm.FuncProto{
		Name:      "<main>",
		Constants: []runtime.Value{runtime.StringValue("helper")},
		Protos:    []*vm.FuncProto{helper},
		Code: []uint32{
			vm.EncodeABx(vm.OP_CLOSURE, 1, 0),
			vm.EncodeABx(vm.OP_SETGLOBAL, 1, 0),
			vm.EncodeAsBx(vm.OP_LOADINT, 1, 1),
			vm.EncodeABx(vm.OP_SETGLOBAL, 1, 0),
		},
	}

	globals := buildProtoStableGlobals(top)
	if _, ok := globals["helper"]; ok {
		t.Fatalf("did not expect reassigned helper to be treated as stable")
	}
}
