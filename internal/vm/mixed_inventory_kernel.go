package vm

import (
	"fmt"
	"math"

	"github.com/gscript/gscript/internal/runtime"
)

func isMixedInventoryOrdersProto(p *FuncProto) bool {
	if p == nil || p.IsVarArg || p.NumParams != 3 || p.MaxStack != 24 ||
		len(p.Code) != 130 || len(p.Constants) != 16 || len(p.Protos) != 0 {
		return false
	}
	wantStrings := map[int]string{
		0:  "string",
		1:  "format",
		2:  "SKU%05d",
		3:  "by_sku",
		4:  "stock",
		5:  "reserved",
		6:  "sold",
		7:  "math",
		8:  "floor",
		9:  "price",
		13: "reorder",
		14: "%s:%d:%d:%.2f",
		15: "sku",
	}
	for idx, want := range wantStrings {
		if !p.Constants[idx].IsString() || p.Constants[idx].Str() != want {
			return false
		}
	}
	if !p.Constants[10].IsFloat() || p.Constants[10].Float() != 100.0 {
		return false
	}
	if !p.Constants[11].IsInt() || p.Constants[11].Int() != 1000000007 {
		return false
	}
	if !p.Constants[12].IsFloat() || p.Constants[12].Float() != 0.5 {
		return false
	}

	code := p.Code
	return DecodeOp(code[5]) == OP_FORPREP &&
		DecodeOp(code[15]) == OP_CALL &&
		DecodeOp(code[18]) == OP_GETTABLE &&
		DecodeOp(code[32]) == OP_SETFIELD &&
		DecodeOp(code[35]) == OP_SETFIELD &&
		DecodeOp(code[42]) == OP_CALL &&
		DecodeOp(code[57]) == OP_SETFIELD &&
		DecodeOp(code[76]) == OP_SETFIELD &&
		DecodeOp(code[96]) == OP_CALL &&
		DecodeOp(code[111]) == OP_FORLOOP &&
		DecodeOp(code[127]) == OP_FORLOOP &&
		DecodeOp(code[129]) == OP_RETURN
}

func (vm *VM) runMixedInventoryOrdersKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil ||
		len(args) != 3 || !args[0].IsTable() || !args[1].IsInt() || !args[2].IsInt() {
		return false, nil, nil
	}
	itemCount := args[1].Int()
	orders := args[2].Int()
	if itemCount <= 0 || itemCount > maxWholeCallScalarScratch || orders < 0 {
		return false, nil, nil
	}
	inv := args[0].Table()
	itemsValue := inv.RawGetString("items")
	bySKUValue := inv.RawGetString("by_sku")
	if !itemsValue.IsTable() || !bySKUValue.IsTable() {
		return false, nil, nil
	}
	items := itemsValue.Table()
	bySKU := bySKUValue.Table()
	index := vm.wholeCallValueScratch(int(itemCount) + 1)
	for i := int64(1); i <= itemCount; i++ {
		itemValue := items.RawGetInt(i)
		if !itemValue.IsTable() {
			return false, nil, nil
		}
		sku := mixedInventorySKU(i)
		if itemValue.Table().RawGetString("sku").Str() != sku {
			return false, nil, nil
		}
		if bySKU.RawGetString(sku) != itemValue {
			return false, nil, nil
		}
		index[i] = itemValue
	}

	var stockCache, reservedCache, soldCache, priceCache, reorderCache, skuCache runtime.FieldCacheEntry
	checksum := int64(0)
	reportLengths := make([]int, 0, orders/2500)
	const mod = int64(1000000007)
	for i := int64(1); i <= orders; i++ {
		idx := (i*37)%itemCount + 1
		item := index[idx].Table()
		qty := (i*11)%6 + 1
		stock := item.RawGetStringCached("stock", &stockCache).Int()
		reserved := item.RawGetStringCached("reserved", &reservedCache).Int()
		available := stock - reserved

		if available >= qty {
			stock -= qty
			item.RawSetStringCached("stock", runtime.IntValue(stock), &stockCache)
			sold := item.RawGetStringCached("sold", &soldCache).Int() + qty
			item.RawSetStringCached("sold", runtime.IntValue(sold), &soldCache)
			price := item.RawGetStringCached("price", &priceCache).Number()
			checksum = (checksum + int64(math.Floor(price*float64(qty)*100.0)) + sold + idx) % mod
		} else {
			reserved = int64(math.Floor(float64(reserved) * 0.5))
			item.RawSetStringCached("reserved", runtime.IntValue(reserved), &reservedCache)
			checksum = (checksum + idx + stock + reserved) % mod
		}

		stock = item.RawGetStringCached("stock", &stockCache).Int()
		reorder := item.RawGetStringCached("reorder", &reorderCache).Int()
		if stock < reorder {
			stock = stock + 120 + (idx % 30)
			item.RawSetStringCached("stock", runtime.IntValue(stock), &stockCache)
			checksum = (checksum + stock*3) % mod
		}

		if i%2500 == 0 {
			sku := item.RawGetStringCached("sku", &skuCache).Str()
			stock = item.RawGetStringCached("stock", &stockCache).Int()
			sold := item.RawGetStringCached("sold", &soldCache).Int()
			price := item.RawGetStringCached("price", &priceCache).Number()
			report := fmt.Sprintf("%s:%d:%d:%.2f", sku, stock, sold, price)
			reportLengths = append(reportLengths, len(report))
			checksum = (checksum + int64(len(report))*17) % mod
		}
	}
	for i, l := range reportLengths {
		checksum = (checksum + int64(l) + int64(i+1)*31) % mod
	}
	cl.Proto.EnteredTier2 = 1
	return true, runtime.ReuseValueSlice1(nil, runtime.IntValue(checksum)), nil
}

func mixedInventorySKU(n int64) string {
	if n >= 0 && n <= 99999 {
		return fmt.Sprintf("SKU%05d", n)
	}
	return fmt.Sprintf("SKU%d", n)
}
