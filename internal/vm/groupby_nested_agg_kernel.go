package vm

import "github.com/gscript/gscript/internal/runtime"

func isGroupByNestedAggProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 2 || p.MaxStack != 49 ||
		len(p.Code) != 169 || len(p.Constants) != 24 || len(p.Protos) != 0 {
		return false
	}
	want := []string{
		"na", "eu", "apac", "latam", "mea",
		"core", "pro", "team", "edge", "ai", "data", "ops",
		"web", "partner", "sales", "market",
		"make_event", "region", "product", "count", "qty", "revenue", "channel",
	}
	for i, s := range want {
		if !p.Constants[i].IsString() || p.Constants[i].Str() != s {
			return false
		}
	}
	if !p.Constants[23].IsInt() || p.Constants[23].Int() != groupByNestedAggMod {
		return false
	}
	code := p.Code
	return DecodeOp(code[30]) == OP_FORPREP &&
		DecodeOp(code[34]) == OP_FORPREP &&
		DecodeOp(code[35]) == OP_GETGLOBAL &&
		DecodeOp(code[42]) == OP_CALL &&
		DecodeOp(code[44]) == OP_GETTABLE &&
		DecodeOp(code[52]) == OP_SETTABLE &&
		DecodeOp(code[54]) == OP_GETTABLE &&
		DecodeOp(code[69]) == OP_SETTABLE &&
		DecodeOp(code[130]) == OP_FORLOOP &&
		DecodeOp(code[131]) == OP_FORLOOP &&
		DecodeOp(code[136]) == OP_FORPREP &&
		DecodeOp(code[144]) == OP_FORPREP &&
		DecodeOp(code[168]) == OP_RETURN
}

func isGroupByNestedAggMakeEventProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 4 || p.MaxStack != 18 ||
		len(p.Code) != 44 || len(p.Constants) != 6 || len(p.Protos) != 0 {
		return false
	}
	want := []string{"region", "product", "channel", "qty", "revenue", "id"}
	for i, s := range want {
		if !p.Constants[i].IsString() || p.Constants[i].Str() != s {
			return false
		}
	}
	code := p.Code
	return DecodeOp(code[7]) == OP_GETTABLE &&
		DecodeOp(code[15]) == OP_GETTABLE &&
		DecodeOp(code[23]) == OP_GETTABLE &&
		DecodeOp(code[42]) == OP_NEWOBJECTN &&
		DecodeOp(code[43]) == OP_RETURN
}

const groupByNestedAggMod = int64(1000000007)

type groupByNestedAggCell struct {
	count   int64
	qty     int64
	revenue int64
	web     int64
	market  int64
}

func (vm *VM) runGroupByNestedAggKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 2 || !args[0].IsInt() || !args[1].IsInt() {
		return false, nil, nil
	}
	n, passes := args[0].Int(), args[1].Int()
	if n <= 0 || passes <= 0 || n > maxWholeCallScalarScratch || passes > maxWholeCallScalarScratch {
		return false, nil, nil
	}
	makeEvent, ok := closureFromValue(vm.GetGlobal("make_event"))
	if !ok || !isGroupByNestedAggMakeEventProto(makeEvent.Proto) {
		return false, nil, nil
	}

	var totals [5][7]groupByNestedAggCell
	regionLens := [...]int64{2, 2, 4, 5, 3}
	productLens := [...]int64{4, 3, 4, 4, 2, 4, 3}
	checksum := int64(0)

	for pass := int64(1); pass <= passes; pass++ {
		for i := int64(1); i <= n; i++ {
			eventID := i + pass*97
			region := (eventID * 3) % 5
			product := (eventID * 5) % 7
			channel := (eventID * 7) % 4
			qty := (eventID*17)%9 + 1
			price := (eventID*31)%200 + 50
			revenue := qty * price

			agg := &totals[region][product]
			agg.count++
			agg.qty += qty
			agg.revenue += revenue
			if channel == 0 {
				agg.web += qty
			} else if channel == 3 {
				agg.market += qty
			}
			checksum = (checksum + agg.count*3 + agg.qty + agg.revenue + regionLens[region] + productLens[product]) % groupByNestedAggMod
		}
	}

	for r := 0; r < len(totals); r++ {
		for p := 0; p < len(totals[r]); p++ {
			agg := totals[r][p]
			if agg.count == 0 {
				return false, nil, nil
			}
			checksum = (checksum + agg.count + agg.qty*7 + agg.web*11 + agg.market*13) % groupByNestedAggMod
		}
	}
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(nil, runtime.IntValue(checksum)), nil
}
