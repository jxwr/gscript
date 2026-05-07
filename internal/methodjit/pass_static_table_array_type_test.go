package methodjit

import "testing"

func TestStaticTableArrayElementTypePass_PropagatesStringLoadType(t *testing.T) {
	fn, tbl, _ := staticTableLenTestFunction(staticTableLenTestOpts{stringValues: true})
	b := fn.Entry
	key := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	header := &Instr{ID: fn.newValueID(), Op: OpTableArrayHeader, Type: TypeInt, Args: []*Value{tbl.Value()}, Block: b}
	data := &Instr{ID: fn.newValueID(), Op: OpTableArrayData, Type: TypeInt, Args: []*Value{header.Value()}, Block: b}
	load := &Instr{ID: fn.newValueID(), Op: OpTableArrayLoad, Type: TypeUnknown, Args: []*Value{data.Value(), key.Value(), key.Value()}, Block: b}
	insertBeforeTerminator(b, key)
	insertBeforeTerminator(b, header)
	insertBeforeTerminator(b, data)
	insertBeforeTerminator(b, load)

	if _, err := StaticTableArrayElementTypePass(fn); err != nil {
		t.Fatalf("StaticTableArrayElementTypePass: %v", err)
	}
	if load.Type != TypeString {
		t.Fatalf("expected lowered static string array load to become TypeString, got %s", load.Type)
	}
}

func TestStaticTableArrayElementTypePass_LeavesMixedTypeUnknown(t *testing.T) {
	fn, tbl, _ := staticTableLenTestFunction(staticTableLenTestOpts{})
	b := fn.Entry
	key := &Instr{ID: fn.newValueID(), Op: OpConstInt, Type: TypeInt, Aux: 1, Block: b}
	load := &Instr{ID: fn.newValueID(), Op: OpGetTable, Type: TypeUnknown, Args: []*Value{tbl.Value(), key.Value()}, Block: b}
	insertBeforeTerminator(b, key)
	insertBeforeTerminator(b, load)

	if _, err := StaticTableArrayElementTypePass(fn); err != nil {
		t.Fatalf("StaticTableArrayElementTypePass: %v", err)
	}
	if load.Type != TypeInt {
		t.Fatalf("expected homogeneous int SetList load to become TypeInt, got %s", load.Type)
	}
}
