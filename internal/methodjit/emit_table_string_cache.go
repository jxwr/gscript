//go:build darwin && arm64

package methodjit

import (
	"github.com/gscript/gscript/internal/jit"
	"github.com/gscript/gscript/internal/runtime"
	"github.com/gscript/gscript/internal/vm"
)

func (ec *emitContext) emitDynamicStringGetTableCache(instr *Instr, doneLabel string) {
	if !ec.shouldEmitDynamicStringKeyCache(instr) {
		return
	}
	asm := ec.asm
	keyID := instr.Args[1].ID
	keyReg := ec.resolveValueNB(keyID, jit.X1)
	if keyReg != jit.X1 {
		asm.MOVreg(jit.X1, keyReg)
	}
	missLabel := ec.uniqueLabel("gettable_string_cache_miss")
	deoptLabel := ec.uniqueLabel("gettable_string_type_deopt")
	ec.emitDynamicStringCacheOrSmallScan(instr, missLabel, func(fieldIdxReg jit.Reg) {
		asm.LDR(jit.X10, jit.X0, jit.TableOffSvals)
		asm.LDRreg(jit.X16, jit.X10, fieldIdxReg)
		ec.emitNativeStringQueryCacheStore(jit.X16)
		ec.emitStoreDynamicStringTableLoad(instr, jit.X16, deoptLabel)
		asm.B(doneLabel)
	}, dynamicStringCacheHandlers{
		valueHit: func(valueReg jit.Reg) {
			ec.emitStoreDynamicStringTableLoad(instr, valueReg, deoptLabel)
			asm.B(doneLabel)
		},
		notFound: func() {
			asm.LoadImm64(jit.X0, nb64(jit.NB_ValNil))
			ec.emitStoreDynamicStringTableLoad(instr, jit.X0, deoptLabel)
			asm.B(doneLabel)
		},
	})
	asm.Label(deoptLabel)
	ec.emitDeopt(instr)
	asm.Label(missLabel)
}

func (ec *emitContext) emitDynamicStringSetTableCache(instr *Instr, doneLabel string) {
	if !ec.shouldEmitDynamicStringKeyCache(instr) || len(instr.Args) < 3 {
		return
	}
	asm := ec.asm
	keyID := instr.Args[1].ID
	keyReg := ec.resolveValueNB(keyID, jit.X1)
	if keyReg != jit.X1 {
		asm.MOVreg(jit.X1, keyReg)
	}
	missLabel := ec.uniqueLabel("settable_string_cache_miss")
	ec.emitDynamicStringCacheOrSmallScan(instr, missLabel, func(fieldIdxReg jit.Reg) {
		valReg := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
		if valReg != jit.X4 {
			asm.MOVreg(jit.X4, valReg)
		}
		asm.LoadImm64(jit.X5, nb64(jit.NB_ValNil))
		asm.CMPreg(jit.X4, jit.X5)
		asm.BCond(jit.CondEQ, missLabel)
		asm.LDR(jit.X10, jit.X0, jit.TableOffSvals)
		asm.STRreg(jit.X4, jit.X10, fieldIdxReg)
		ec.emitBumpTableStringLookupVersion(jit.X0, jit.X5)
		asm.MOVimm16(jit.X5, 1)
		asm.STRB(jit.X5, jit.X0, jit.TableOffKeysDirty)
		asm.B(doneLabel)
	}, dynamicStringCacheHandlers{
		appendHit: func(fieldIdxReg, entryReg jit.Reg) {
			valReg := ec.resolveValueNB(instr.Args[2].ID, jit.X4)
			if valReg != jit.X4 {
				asm.MOVreg(jit.X4, valReg)
			}
			asm.LoadImm64(jit.X5, nb64(jit.NB_ValNil))
			asm.CMPreg(jit.X4, jit.X5)
			asm.BCond(jit.CondEQ, missLabel)
			asm.LDR(jit.X5, entryReg, tableStringKeyCacheEntryAppendShape)
			asm.CBZ(jit.X5, missLabel)
			asm.LDR(jit.X6, jit.X0, jit.TableOffSmap)
			asm.CBNZ(jit.X6, missLabel)
			asm.LDR(jit.X6, jit.X0, jit.TableOffLazyTree)
			asm.CBNZ(jit.X6, missLabel)
			asm.LDR(jit.X6, jit.X0, jit.TableOffSvalsLen)
			asm.CMPreg(fieldIdxReg, jit.X6)
			asm.BCond(jit.CondNE, missLabel)
			asm.CMPimm(fieldIdxReg, runtime.SmallFieldCap)
			asm.BCond(jit.CondGE, missLabel)
			asm.LDR(jit.X7, jit.X0, jit.TableOffSvals+16)
			asm.CMPreg(fieldIdxReg, jit.X7)
			asm.BCond(jit.CondGE, missLabel)
			asm.LDR(jit.X7, jit.X0, jit.TableOffSvals)
			asm.STRreg(jit.X4, jit.X7, fieldIdxReg)
			ec.emitBumpTableStringLookupVersion(jit.X0, jit.X7)
			asm.ADDimm(jit.X6, jit.X6, 1)
			asm.STR(jit.X6, jit.X0, jit.TableOffSvalsLen)
			asm.LDRW(jit.X7, entryReg, tableStringKeyCacheEntryShapeID)
			asm.STRW(jit.X7, jit.X0, jit.TableOffShapeID)
			asm.STR(jit.X5, jit.X0, jit.TableOffShape)
			asm.LDR(jit.X7, jit.X5, shapeOffFieldKeys)
			asm.STR(jit.X7, jit.X0, jit.TableOffSkeys)
			asm.LDR(jit.X7, jit.X5, shapeOffFieldKeysLen)
			asm.STR(jit.X7, jit.X0, jit.TableOffSkeysLen)
			asm.LDR(jit.X7, jit.X5, shapeOffFieldKeysCap)
			asm.STR(jit.X7, jit.X0, jit.TableOffSkeys+16)
			asm.MOVimm16(jit.X7, 1)
			asm.STRB(jit.X7, jit.X0, jit.TableOffKeysDirty)
			asm.B(doneLabel)
		},
	})
	asm.Label(missLabel)
}

func (ec *emitContext) emitStoreDynamicStringTableLoad(instr *Instr, valReg jit.Reg, deoptLabel string) {
	asm := ec.asm
	switch instr.Type {
	case TypeInt:
		asm.LSRimm(jit.X2, valReg, 48)
		asm.MOVimm16(jit.X3, uint16(jit.NB_TagIntShr48))
		asm.CMPreg(jit.X2, jit.X3)
		asm.BCond(jit.CondNE, deoptLabel)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		asm.SBFX(jit.X0, jit.X0, 0, 48)
		ec.storeRawInt(jit.X0, instr.ID)
	case TypeFloat:
		jit.EmitIsTagged(asm, valReg, jit.X2)
		asm.BCond(jit.CondEQ, deoptLabel)
		asm.FMOVtoFP(jit.D0, valReg)
		ec.storeRawFloat(jit.D0, instr.ID)
	case TypeTable:
		jit.EmitCheckIsTableFull(asm, valReg, jit.X2, jit.X3, deoptLabel)
		ec.storeResultNB(valReg, instr.ID)
	default:
		ec.storeResultNB(valReg, instr.ID)
	}
}

func (ec *emitContext) shouldEmitDynamicStringKeyCache(instr *Instr) bool {
	if instr == nil || len(instr.Args) < 2 || !instr.HasSource || instr.SourcePC < 0 {
		return false
	}
	if ec.fn == nil || !protoHasDynamicStringKeyCacheAt(ec.fn.Proto, instr.SourcePC) {
		if ec.fn == nil || ec.fn.Proto == nil {
			return false
		}
		if instr.SourcePC < len(ec.fn.Proto.Feedback) &&
			ec.fn.Proto.Feedback[instr.SourcePC].Right == vm.FBString {
			return true
		}
		// Some late loops can compile before their dynamic key sites have
		// feedback. Emit the string-key probe whenever the key is not proven
		// int; non-string keys fall through to the existing array path and
		// preserve the old fallback behavior. Writes only handle existing
		// small-table fields here; new-key append still falls back through the
		// normal SetTable exit.
		return (instr.Op == OpGetTable || instr.Op == OpSetTable) && !tableKeyProvenInt(instr.Args[1])
	}
	return true
}

type dynamicStringCacheHandlers struct {
	valueHit  func(jit.Reg)
	notFound  func()
	appendHit func(fieldIdxReg, entryReg jit.Reg)
}

func (ec *emitContext) emitDynamicStringCacheOrSmallScan(instr *Instr, missLabel string, hit func(fieldIdxReg jit.Reg), options ...dynamicStringCacheHandlers) {
	asm := ec.asm
	var handlers dynamicStringCacheHandlers
	if len(options) > 0 {
		handlers = options[0]
	}

	jit.EmitCheckIsString(asm, jit.X1, jit.X2, jit.X3, missLabel)
	jit.EmitExtractPtr(asm, jit.X4, jit.X1) // X4 = *string header
	asm.CBZ(jit.X4, missLabel)
	asm.LDR(jit.X5, jit.X4, 0) // X5 = key data
	asm.LDR(jit.X6, jit.X4, 8) // X6 = key len

	asm.LDR(jit.X8, jit.X0, jit.TableOffLazyTree)
	asm.CBNZ(jit.X8, missLabel)
	asm.LDRW(jit.X7, jit.X0, jit.TableOffShapeID)
	smapCacheLabel := ec.uniqueLabel("dyn_string_smap_cache")
	if handlers.appendHit == nil {
		asm.CBZ(jit.X7, smapCacheLabel)
	}

	if handlers.valueHit != nil {
		queryMissLabel := ec.uniqueLabel("dyn_string_query_cache_miss")
		ec.emitNativeStringQueryCacheProbe(queryMissLabel, func(valueReg jit.Reg) {
			handlers.valueHit(valueReg)
		})
		asm.Label(queryMissLabel)
	}

	scanLabel := ec.uniqueLabel("dyn_string_scan")
	asm.LDR(jit.X3, mRegCtx, execCtxOffBaselineTableStringKeyCache)
	asm.CBZ(jit.X3, scanLabel)
	entryOff := instr.SourcePC * runtime.TableStringKeyCacheWays * tableStringKeyCacheEntrySize
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X3, jit.X3, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X8, int64(entryOff))
			asm.ADDreg(jit.X3, jit.X3, jit.X8)
		}
	}

	cacheLoopLabel := ec.uniqueLabel("dyn_string_cache_loop")
	cacheNextLabel := ec.uniqueLabel("dyn_string_cache_next")
	asm.MOVimm16(jit.X9, 0)
	asm.Label(cacheLoopLabel)
	asm.LDR(jit.X10, jit.X3, tableStringKeyCacheEntryKeyData)
	asm.CMPreg(jit.X10, jit.X5)
	asm.BCond(jit.CondNE, cacheNextLabel)
	asm.LDR(jit.X10, jit.X3, tableStringKeyCacheEntryKeyLen)
	asm.CMPreg(jit.X10, jit.X6)
	asm.BCond(jit.CondNE, cacheNextLabel)
	asm.LDRW(jit.X10, jit.X3, tableStringKeyCacheEntryShapeID)
	asm.CMPreg(jit.X10, jit.X7)
	if handlers.appendHit == nil {
		asm.BCond(jit.CondNE, cacheNextLabel)
	} else {
		appendCheckLabel := ec.uniqueLabel("dyn_string_cache_append_check")
		asm.BCond(jit.CondNE, appendCheckLabel)
		asm.B(appendCheckLabel + "_done")
		asm.Label(appendCheckLabel)
		asm.LDRW(jit.X10, jit.X3, tableStringKeyCacheEntryAppendShapeID)
		asm.CMPreg(jit.X10, jit.X7)
		asm.BCond(jit.CondNE, cacheNextLabel)
		asm.LDR(jit.X11, jit.X3, tableStringKeyCacheEntryFieldIdx)
		handlers.appendHit(jit.X11, jit.X3)
		asm.Label(appendCheckLabel + "_done")
	}
	asm.LDR(jit.X11, jit.X3, tableStringKeyCacheEntryFieldIdx)
	asm.LDR(jit.X10, jit.X0, jit.TableOffSvalsLen)
	asm.CMPreg(jit.X11, jit.X10)
	asm.BCond(jit.CondGE, scanLabel)
	hit(jit.X11)

	asm.Label(cacheNextLabel)
	asm.ADDimm(jit.X3, jit.X3, uint16(tableStringKeyCacheEntrySize))
	asm.ADDimm(jit.X9, jit.X9, 1)
	asm.CMPimm(jit.X9, runtime.TableStringKeyCacheWays)
	asm.BCond(jit.CondLT, cacheLoopLabel)

	// Cache associativity is deliberately small. On polymorphic shaped tables
	// (for example, several tables sharing the same key set in different append
	// orders), avoid a per-lookup exit by scanning the small shaped string-key
	// slice natively. Large smap/hash-mode tables keep shapeID zero and fall
	// through to the normal table exit.
	asm.Label(scanLabel)
	asm.LDR(jit.X10, jit.X0, jit.TableOffSkeysLen)
	emptyShapeLabel := missLabel
	if handlers.notFound != nil {
		emptyShapeLabel = ec.uniqueLabel("dyn_string_scan_empty")
	}
	asm.CBZ(jit.X10, emptyShapeLabel)
	asm.LDR(jit.X11, jit.X0, jit.TableOffSkeys)
	asm.CBZ(jit.X11, missLabel)

	scanLoopLabel := ec.uniqueLabel("dyn_string_scan_loop")
	scanNextLabel := ec.uniqueLabel("dyn_string_scan_next")
	byteLoopLabel := ec.uniqueLabel("dyn_string_scan_bytes")
	foundLabel := ec.uniqueLabel("dyn_string_scan_found")
	asm.MOVimm16(jit.X9, 0) // field index
	asm.Label(scanLoopLabel)
	asm.CMPreg(jit.X9, jit.X10)
	missingLabel := missLabel
	if handlers.notFound != nil {
		missingLabel = ec.uniqueLabel("dyn_string_scan_missing")
	}
	asm.BCond(jit.CondGE, missingLabel)
	asm.LSLimm(jit.X12, jit.X9, 4) // Go string header is two machine words.
	asm.ADDreg(jit.X12, jit.X11, jit.X12)
	asm.LDR(jit.X13, jit.X12, 0) // candidate data
	asm.LDR(jit.X14, jit.X12, 8) // candidate len
	asm.CMPreg(jit.X14, jit.X6)
	asm.BCond(jit.CondNE, scanNextLabel)
	asm.CMPreg(jit.X13, jit.X5)
	asm.BCond(jit.CondEQ, foundLabel)
	asm.CBZ(jit.X14, foundLabel)
	asm.MOVimm16(jit.X15, 0) // byte index
	asm.Label(byteLoopLabel)
	asm.LDRBreg(jit.X16, jit.X13, jit.X15)
	asm.LDRBreg(jit.X17, jit.X5, jit.X15)
	asm.CMPreg(jit.X16, jit.X17)
	asm.BCond(jit.CondNE, scanNextLabel)
	asm.ADDimm(jit.X15, jit.X15, 1)
	asm.CMPreg(jit.X15, jit.X14)
	asm.BCond(jit.CondLT, byteLoopLabel)

	asm.Label(foundLabel)
	ec.emitRememberDynamicStringScanHit(instr, jit.X9, jit.X5, jit.X6, jit.X7)
	asm.MOVreg(jit.X11, jit.X9)
	hit(jit.X11)

	asm.Label(scanNextLabel)
	asm.ADDimm(jit.X9, jit.X9, 1)
	asm.B(scanLoopLabel)
	if handlers.notFound != nil {
		asm.Label(emptyShapeLabel)
		handlers.notFound()
		asm.Label(missingLabel)
		handlers.notFound()
	}

	asm.Label(smapCacheLabel)
	if handlers.valueHit == nil {
		asm.B(missLabel)
		return
	}
	asm.LDR(jit.X8, jit.X0, jit.TableOffStringLookupCache)
	asm.CBZ(jit.X8, missLabel)
	asm.LDR(jit.X3, jit.X8, jit.StringLookupCacheOffEntries)
	asm.CBZ(jit.X3, missLabel)
	asm.LDR(jit.X10, jit.X8, jit.StringLookupCacheOffMask)

	useQueryCache := handlers.valueHit != nil
	if useQueryCache {
		queryMissLabel := ec.uniqueLabel("dyn_string_query_cache_miss")
		ec.emitNativeStringQueryCacheProbe(queryMissLabel, func(valueReg jit.Reg) {
			handlers.valueHit(valueReg)
		})
		asm.Label(queryMissLabel)
	}

	ec.emitStringLookupContentHash(jit.X5, jit.X6, jit.X9, jit.X11, jit.X14, jit.X15, "dyn_string_smap_hash")
	asm.MOVreg(jit.X15, jit.X9)
	asm.ANDreg(jit.X9, jit.X9, jit.X10)

	smapLoopLabel := ec.uniqueLabel("dyn_string_smap_loop")
	smapNextLabel := ec.uniqueLabel("dyn_string_smap_next")
	smapFoundLabel := ec.uniqueLabel("dyn_string_smap_found")
	smapByteLoopLabel := ec.uniqueLabel("dyn_string_smap_bytes")
	asm.MOVimm16(jit.X13, 0)
	asm.Label(smapLoopLabel)
	asm.ADDreg(jit.X11, jit.X9, jit.X13)
	asm.ANDreg(jit.X11, jit.X11, jit.X10)
	asm.LSLimm(jit.X12, jit.X11, 6) // idx * 64
	asm.ADDreg(jit.X12, jit.X3, jit.X12)
	asm.LDRB(jit.X14, jit.X12, jit.StringLookupCacheEntryOffValid)
	asm.CBZ(jit.X14, missLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffHash)
	asm.CMPreg(jit.X14, jit.X15)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffKeyLen)
	asm.CMPreg(jit.X14, jit.X6)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.LDR(jit.X14, jit.X12, jit.StringLookupCacheEntryOffKeyData)
	asm.CMPimm(jit.X6, 8)
	asm.BCond(jit.CondEQ, smapByteLoopLabel+"_len8")
	asm.CMPreg(jit.X14, jit.X5)
	asm.BCond(jit.CondEQ, smapFoundLabel)
	asm.CBZ(jit.X6, smapFoundLabel)
	asm.MOVimm16(jit.X15, 0)
	asm.B(smapByteLoopLabel)
	asm.Label(smapByteLoopLabel + "_len8")
	asm.LDR(jit.X16, jit.X14, 0)
	asm.LDR(jit.X17, jit.X5, 0)
	asm.CMPreg(jit.X16, jit.X17)
	asm.BCond(jit.CondEQ, smapFoundLabel)
	asm.B(smapNextLabel)
	asm.Label(smapByteLoopLabel)
	asm.LDRBreg(jit.X16, jit.X14, jit.X15)
	asm.LDRBreg(jit.X17, jit.X5, jit.X15)
	asm.CMPreg(jit.X16, jit.X17)
	asm.BCond(jit.CondNE, smapNextLabel)
	asm.ADDimm(jit.X15, jit.X15, 1)
	asm.CMPreg(jit.X15, jit.X6)
	asm.BCond(jit.CondLT, smapByteLoopLabel)
	asm.Label(smapFoundLabel)
	asm.LDR(jit.X16, jit.X12, jit.StringLookupCacheEntryOffValue)
	if useQueryCache {
		ec.emitNativeStringQueryCacheStore(jit.X16)
	}
	handlers.valueHit(jit.X16)

	asm.Label(smapNextLabel)
	asm.ADDimm(jit.X13, jit.X13, 1)
	asm.CMPimm(jit.X13, runtime.StringLookupCacheProbeLimit)
	asm.BCond(jit.CondLT, smapLoopLabel)
	asm.B(missLabel)
}

func (ec *emitContext) emitRememberDynamicStringScanHit(instr *Instr, fieldIdxReg, keyDataReg, keyLenReg, shapeIDReg jit.Reg) {
	if ec == nil || instr == nil || !instr.HasSource || instr.SourcePC < 0 {
		return
	}
	asm := ec.asm
	skipLabel := ec.uniqueLabel("dyn_string_remember_skip")
	asm.LDR(jit.X12, mRegCtx, execCtxOffBaselineTableStringKeyCache)
	asm.CBZ(jit.X12, skipLabel)
	entryOff := instr.SourcePC * runtime.TableStringKeyCacheWays * tableStringKeyCacheEntrySize
	if entryOff > 0 {
		if entryOff <= 4095 {
			asm.ADDimm(jit.X12, jit.X12, uint16(entryOff))
		} else {
			asm.LoadImm64(jit.X13, int64(entryOff))
			asm.ADDreg(jit.X12, jit.X12, jit.X13)
		}
	}

	// Use a deterministic way rather than always clobbering way 0. The key is
	// based on the same stable identity the probe uses: table shape plus string
	// data pointer and length.
	asm.EORreg(jit.X13, keyDataReg, keyLenReg)
	asm.EORreg(jit.X13, jit.X13, shapeIDReg)
	asm.LoadImm64(jit.X14, int64(runtime.TableStringKeyCacheWays-1))
	asm.ANDreg(jit.X13, jit.X13, jit.X14)
	asm.LoadImm64(jit.X14, int64(tableStringKeyCacheEntrySize))
	asm.MUL(jit.X13, jit.X13, jit.X14)
	asm.ADDreg(jit.X12, jit.X12, jit.X13)

	asm.STR(keyDataReg, jit.X12, tableStringKeyCacheEntryKeyData)
	asm.STR(keyLenReg, jit.X12, tableStringKeyCacheEntryKeyLen)
	asm.STR(fieldIdxReg, jit.X12, tableStringKeyCacheEntryFieldIdx)
	asm.STRW(shapeIDReg, jit.X12, tableStringKeyCacheEntryShapeID)
	asm.STRW(jit.XZR, jit.X12, tableStringKeyCacheEntryAppendShapeID)
	asm.STR(jit.XZR, jit.X12, tableStringKeyCacheEntryAppendShape)
	asm.Label(skipLabel)
}

func dynamicStringQueryCacheUseful(instr *Instr) bool {
	if instr == nil || len(instr.Args) < 2 || instr.Args[1] == nil || instr.Args[1].Def == nil {
		return false
	}
	switch instr.Args[1].Def.Op {
	case OpConstString, OpStringConstLookup, OpStringFormatInt:
		return true
	default:
		return false
	}
}

func (ec *emitContext) emitNativeStringQueryCacheSlot(dst, tmp jit.Reg) {
	asm := ec.asm
	asm.LSRimm(tmp, jit.X5, 4)
	asm.EORreg(dst, tmp, jit.X0)
	asm.EORreg(dst, dst, jit.X6)
	asm.LoadImm64(tmp, int64(runtime.NativeStringQueryCacheSize-1))
	asm.ANDreg(dst, dst, tmp)
	asm.LoadImm64(tmp, int64(nativeStringQueryCacheEntrySize))
	asm.MUL(dst, dst, tmp)
	asm.LoadImm64(tmp, int64(uintptr(runtime.NativeStringQueryCachePtr())))
	asm.ADDreg(dst, tmp, dst)
}

func (ec *emitContext) emitNativeStringQueryCacheProbe(missLabel string, hit func(jit.Reg)) {
	asm := ec.asm
	ec.emitNativeStringQueryCacheSlot(jit.X11, jit.X12)
	asm.LDR(jit.X13, jit.X11, nativeStringQueryCacheEntryTable)
	asm.CMPreg(jit.X13, jit.X0)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X13, jit.X0, jit.TableOffStringLookupVer)
	asm.LDR(jit.X14, jit.X11, nativeStringQueryCacheEntryVersion)
	asm.CMPreg(jit.X14, jit.X13)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X14, jit.X11, nativeStringQueryCacheEntryKeyData)
	asm.CMPreg(jit.X14, jit.X5)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X14, jit.X11, nativeStringQueryCacheEntryKeyLen)
	asm.CMPreg(jit.X14, jit.X6)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X14, jit.X11, nativeStringQueryCacheEntryValue)
	hit(jit.X14)
}

func (ec *emitContext) emitNativeStringQueryCacheStore(valueReg jit.Reg) {
	asm := ec.asm
	ec.emitNativeStringQueryCacheSlot(jit.X11, jit.X13)
	asm.LDR(jit.X14, jit.X0, jit.TableOffStringLookupVer)
	queryVersionReadyLabel := ec.uniqueLabel("native_string_query_version_ready")
	asm.CBNZ(jit.X14, queryVersionReadyLabel)
	asm.MOVimm16(jit.X14, 1)
	asm.STR(jit.X14, jit.X0, jit.TableOffStringLookupVer)
	asm.Label(queryVersionReadyLabel)
	asm.STR(jit.X0, jit.X11, nativeStringQueryCacheEntryTable)
	asm.STR(jit.X14, jit.X11, nativeStringQueryCacheEntryVersion)
	asm.STR(jit.X5, jit.X11, nativeStringQueryCacheEntryKeyData)
	asm.STR(jit.X6, jit.X11, nativeStringQueryCacheEntryKeyLen)
	asm.STR(valueReg, jit.X11, nativeStringQueryCacheEntryValue)
}

func (ec *emitContext) emitFormattedIntQueryCacheSlot(dst, tmp jit.Reg, pattern string, intReg jit.Reg) {
	asm := ec.asm
	asm.LoadImm64(tmp, int64(stringDataPtr(pattern)))
	asm.EORreg(dst, tmp, jit.X0)
	asm.EORreg(dst, dst, intReg)
	asm.LoadImm64(tmp, int64(runtime.NativeFormattedIntQueryCacheSize-1))
	asm.ANDreg(dst, dst, tmp)
	asm.LoadImm64(tmp, int64(nativeFormattedIntQueryCacheEntrySize))
	asm.MUL(dst, dst, tmp)
	asm.LoadImm64(tmp, int64(uintptr(runtime.NativeFormattedIntQueryCachePtr())))
	asm.ADDreg(dst, tmp, dst)
}

func (ec *emitContext) emitGetTableStringFormatIntNative(instr *Instr) {
	if instr == nil || len(instr.Args) != 4 || ec.fn == nil {
		ec.emitGetTableStringFormatIntExit(instr)
		return
	}
	patternIdx := int(instr.Aux)
	if patternIdx < 0 || patternIdx >= len(ec.fn.StringFormatPatterns) {
		ec.emitGetTableStringFormatIntExit(instr)
		return
	}
	pattern := ec.fn.StringFormatPatterns[patternIdx]
	asm := ec.asm
	missLabel := ec.uniqueLabel("fmtint_gettable_miss")
	doneLabel := ec.uniqueLabel("fmtint_gettable_done")

	resultSlot, hasSlot := ec.slotMap[instr.ID]
	if !hasSlot {
		resultSlot = ec.nextSlot
		ec.slotMap[instr.ID] = resultSlot
		ec.nextSlot++
	}
	tempBase := ec.nextSlot
	ec.nextSlot += 4

	tblReg := ec.resolveValueNB(instr.Args[0].ID, jit.X0)
	if tblReg != jit.X0 {
		asm.MOVreg(jit.X0, tblReg)
	}
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, missLabel)
	jit.EmitExtractPtr(asm, jit.X0, jit.X0)
	asm.CBZ(jit.X0, missLabel)
	asm.LDR(jit.X2, jit.X0, jit.TableOffMetatable)
	asm.CBNZ(jit.X2, missLabel)

	callee := ec.resolveValueNB(instr.Args[1].ID, jit.X1)
	if callee != jit.X1 {
		asm.MOVreg(jit.X1, callee)
	}
	ec.emitStdStringFormatGuard(jit.X1, missLabel)
	patternVal := ec.resolveValueNB(instr.Args[2].ID, jit.X1)
	if patternVal != jit.X1 {
		asm.MOVreg(jit.X1, patternVal)
	}
	ec.emitStringValueEqualsConstGuard(jit.X1, pattern, missLabel)
	intVal := ec.resolveValueNB(instr.Args[3].ID, jit.X1)
	if intVal != jit.X1 {
		asm.MOVreg(jit.X1, intVal)
	}
	emitCheckIsInt(asm, jit.X1, jit.X2)
	asm.BCond(jit.CondNE, missLabel)
	jit.EmitUnboxInt(asm, jit.X1, jit.X1)

	ec.emitFormattedIntQueryCacheSlot(jit.X11, jit.X12, pattern, jit.X1)
	asm.LDR(jit.X13, jit.X11, nativeFormattedIntQueryCacheEntryTable)
	asm.CMPreg(jit.X13, jit.X0)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X13, jit.X0, jit.TableOffStringLookupVer)
	asm.LDR(jit.X14, jit.X11, nativeFormattedIntQueryCacheEntryVersion)
	asm.CMPreg(jit.X14, jit.X13)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X14, jit.X11, nativeFormattedIntQueryCacheEntryPatternData)
	asm.LoadImm64(jit.X13, int64(stringDataPtr(pattern)))
	asm.CMPreg(jit.X14, jit.X13)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X14, jit.X11, nativeFormattedIntQueryCacheEntryPatternLen)
	asm.LoadImm64(jit.X13, int64(len(pattern)))
	asm.CMPreg(jit.X14, jit.X13)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X14, jit.X11, nativeFormattedIntQueryCacheEntryN)
	asm.CMPreg(jit.X14, jit.X1)
	asm.BCond(jit.CondNE, missLabel)
	asm.LDR(jit.X0, jit.X11, nativeFormattedIntQueryCacheEntryValue)
	jit.EmitCheckIsTableFull(asm, jit.X0, jit.X2, jit.X3, missLabel)
	ec.setValueRepr(instr.ID, valueReprBoxed)
	ec.storeValue(jit.X0, instr.ID)
	ec.activeRegs[instr.ID] = false
	asm.B(doneLabel)

	asm.Label(missLabel)
	for i, arg := range instr.Args {
		valReg := ec.resolveValueNB(arg.ID, jit.X0)
		if valReg != jit.X0 {
			asm.MOVreg(jit.X0, valReg)
		}
		asm.STR(jit.X0, mRegRegs, slotOffset(tempBase+i))
	}
	ec.emitGetTableStringFormatIntExitFromTemps(instr, resultSlot, tempBase)
	asm.Label(doneLabel)
}

func (ec *emitContext) emitStringLookupContentHash(dataReg, lenReg, dstReg, idxReg, byteReg, primeReg jit.Reg, prefix string) {
	asm := ec.asm
	fast8Label := ec.uniqueLabel(prefix + "_len8")
	loopLabel := ec.uniqueLabel(prefix + "_loop")
	doneLabel := ec.uniqueLabel(prefix + "_done")
	endLabel := ec.uniqueLabel(prefix + "_end")
	asm.LoadImm64(dstReg, int64(1469598103934665603))
	asm.LoadImm64(primeReg, int64(1099511628211))
	asm.CMPimm(lenReg, 8)
	asm.BCond(jit.CondEQ, fast8Label)
	asm.MOVimm16(idxReg, 0)
	asm.Label(loopLabel)
	asm.CMPreg(idxReg, lenReg)
	asm.BCond(jit.CondGE, doneLabel)
	asm.LDRBreg(byteReg, dataReg, idxReg)
	asm.EORreg(dstReg, dstReg, byteReg)
	asm.MUL(dstReg, dstReg, primeReg)
	asm.ADDimm(idxReg, idxReg, 1)
	asm.B(loopLabel)
	asm.Label(doneLabel)
	asm.B(endLabel)

	asm.Label(fast8Label)
	for i := 0; i < 8; i++ {
		asm.LDRB(byteReg, dataReg, i)
		asm.EORreg(dstReg, dstReg, byteReg)
		asm.MUL(dstReg, dstReg, primeReg)
	}
	asm.Label(endLabel)
}
