package methodjit

import (
	"fmt"

	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func allStdStringSubFunctions(vals []runtime.Value) bool {
	if len(vals) == 0 {
		return false
	}
	for _, v := range vals {
		if !runtime.IsStdStringSubFunction(v) {
			return false
		}
	}
	return true
}

func executeStringSplitSubstrFallback(callVM *vm.VM, splitCallee runtime.Value, subCallees []runtime.Value, sv, sepv runtime.Value, specs []StringSplitSubSpec, specIdx int) (runtime.Value, error) {
	if specIdx < 0 || specIdx >= len(specs) {
		return runtime.NilValue(), fmt.Errorf("string.split substring spec out of range")
	}
	spec := specs[specIdx]
	if spec.SubCallCount < 1 || spec.SubCallCount > 2 || len(subCallees) < spec.SubCallCount {
		return runtime.NilValue(), fmt.Errorf("string.split substring fallback has invalid sub call count")
	}
	splitResults, err := callVM.CallValue(splitCallee, []runtime.Value{sv, sepv})
	if err != nil {
		return runtime.NilValue(), err
	}
	current := runtime.NilValue()
	if len(splitResults) > 0 && splitResults[0].IsTable() {
		current = splitResults[0].Table().RawGetInt(spec.TokenIndex)
	}
	ranges := []struct {
		start  int64
		end    int64
		hasEnd bool
	}{
		{start: spec.FirstStart, end: spec.FirstEnd, hasEnd: spec.FirstHasEnd},
		{start: spec.SecondStart, end: spec.SecondEnd, hasEnd: spec.SecondHasEnd},
	}
	for i := 0; i < spec.SubCallCount; i++ {
		args := []runtime.Value{current, runtime.IntValue(ranges[i].start)}
		if ranges[i].hasEnd {
			args = append(args, runtime.IntValue(ranges[i].end))
		}
		subResults, err := callVM.CallValue(subCallees[i], args)
		if err != nil {
			return runtime.NilValue(), err
		}
		if len(subResults) == 0 {
			current = runtime.NilValue()
		} else {
			current = subResults[0]
		}
	}
	return current, nil
}

func executeStringSplitSubstrNumberFallback(callVM *vm.VM, splitCallee runtime.Value, subCallees []runtime.Value, tonumberCallee, sv, sepv runtime.Value, specs []StringSplitSubSpec, specIdx int) (runtime.Value, error) {
	subValue, err := executeStringSplitSubstrFallback(callVM, splitCallee, subCallees, sv, sepv, specs, specIdx)
	if err != nil {
		return runtime.NilValue(), err
	}
	numberResults, err := callVM.CallValue(tonumberCallee, []runtime.Value{subValue})
	if err != nil {
		return runtime.NilValue(), err
	}
	if len(numberResults) == 0 {
		return runtime.NilValue(), nil
	}
	return numberResults[0], nil
}
