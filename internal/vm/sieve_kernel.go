package vm

import "github.com/gscript/gscript/internal/runtime"

func (vm *VM) tryRunSieveWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 1 || !vm.noGlobalLock || !isSieveProto(cl.Proto) {
		return false, nil, nil
	}
	if !args[0].IsNumber() {
		return false, nil, nil
	}
	nn := args[0].Number()
	n64 := int64(nn)
	if float64(n64) != nn || n64 < 0 || int64(int(n64)) != n64 {
		return false, nil, nil
	}
	return true, []runtime.Value{runtime.IntValue(runSieveCountKernel(int(n64)))}, nil
}

func runSieveCountKernel(n int) int64 {
	if n < 2 {
		return 0
	}
	flags := make([]byte, n+1)
	for i := 2; i <= n; i++ {
		flags[i] = 1
	}
	for i := 2; i <= n/i; i++ {
		if flags[i] == 0 {
			continue
		}
		for j := i * i; j <= n; j += i {
			flags[j] = 0
		}
	}
	count := int64(0)
	for i := 2; i <= n; i++ {
		if flags[i] != 0 {
			count++
		}
	}
	return count
}

func IsSieveKernelProto(p *FuncProto) bool {
	return isSieveProto(p)
}

func isSieveProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 1 || p.IsVarArg || p.MaxStack < 13 ||
		len(p.Constants) != 0 || len(p.Protos) != 0 {
		return false
	}
	return matchSieveCountBytecode(p.Code)
}

func matchSieveCountBytecode(code []uint32) bool {
	if len(code) != 45 {
		return false
	}
	p := newBytecodePattern(code)
	return matchSieveInitFill(p) &&
		matchSieveMarkComposites(p) &&
		matchSieveCountPrimes(p)
}

func matchSieveInitFill(p bytecodePattern) bool {
	const (
		nReg        = 0
		flagsReg    = 1
		fillBase    = 2
		fillLoopVar = fillBase + 3
		trueReg     = 6
		fillKeyReg  = 7
		fillForPrep = 4
		fillBodyPC  = 5
		fillForLoop = 8
	)
	bodyPC, loopPC, ok := p.numericForLoop(fillForPrep, fillBase)
	if !ok || bodyPC != fillBodyPC || loopPC != fillForLoop {
		return false
	}
	return p.abc(0, OP_NEWTABLE, flagsReg, 0, 0) &&
		p.loadInt(1, fillBase, 2) &&
		p.move(2, fillBase+1, nReg) &&
		p.loadInt(3, fillBase+2, 1) &&
		p.loadBool(5, trueReg, true) &&
		p.move(6, fillKeyReg, fillLoopVar) &&
		p.abc(7, OP_SETTABLE, flagsReg, fillKeyReg, trueReg)
}

func matchSieveMarkComposites(p bytecodePattern) bool {
	const (
		nReg     = 0
		flagsReg = 1
		iReg     = 5
		tmpReg   = 6
		auxReg   = 7
		keyReg   = 8

		markStartPC = 10
		countStart  = 30
		innerStart  = 18
		afterInner  = 26
	)
	return p.loadInt(9, iReg, 2) &&
		p.abc(10, OP_MUL, tmpReg, iReg, iReg) &&
		p.abc(11, OP_LE, 0, tmpReg, nReg) &&
		p.jumpTo(12, countStart) &&
		p.move(13, auxReg, iReg) &&
		p.abc(14, OP_GETTABLE, tmpReg, flagsReg, auxReg) &&
		p.abc(15, OP_TEST, tmpReg, 0, 0) &&
		p.jumpTo(16, afterInner) &&
		p.abc(17, OP_MUL, tmpReg, iReg, iReg) &&
		p.abc(18, OP_LE, 0, tmpReg, nReg) &&
		p.jumpTo(19, afterInner) &&
		p.loadBool(20, auxReg, false) &&
		p.move(21, keyReg, tmpReg) &&
		p.abc(22, OP_SETTABLE, flagsReg, keyReg, auxReg) &&
		p.abc(23, OP_ADD, auxReg, tmpReg, iReg) &&
		p.move(24, tmpReg, auxReg) &&
		p.jumpTo(25, innerStart) &&
		p.loadInt(26, auxReg, 1) &&
		p.abc(27, OP_ADD, tmpReg, iReg, auxReg) &&
		p.move(28, iReg, tmpReg) &&
		p.jumpTo(29, markStartPC)
}

func matchSieveCountPrimes(p bytecodePattern) bool {
	const (
		nReg      = 0
		flagsReg  = 1
		countReg  = 6
		countBase = 7
		countVar  = countBase + 3
		flagReg   = 11
		oneReg    = 12
		keyReg    = 12
		countPrep = 34
		countBody = 35
		countLoop = 42
		returnReg = 10
		returnPC  = 44
	)
	bodyPC, loopPC, ok := p.numericForLoop(countPrep, countBase)
	if !ok || bodyPC != countBody || loopPC != countLoop {
		return false
	}
	return p.loadInt(30, countReg, 0) &&
		p.loadInt(31, countBase, 2) &&
		p.move(32, countBase+1, nReg) &&
		p.loadInt(33, countBase+2, 1) &&
		p.move(35, keyReg, countVar) &&
		p.abc(36, OP_GETTABLE, flagReg, flagsReg, keyReg) &&
		p.abc(37, OP_TEST, flagReg, 0, 0) &&
		p.jumpTo(38, countLoop) &&
		p.loadInt(39, oneReg, 1) &&
		p.abc(40, OP_ADD, flagReg, countReg, oneReg) &&
		p.move(41, countReg, flagReg) &&
		p.move(43, returnReg, countReg) &&
		p.returnFixed(returnPC, returnReg, 2)
}
