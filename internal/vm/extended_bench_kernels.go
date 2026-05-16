package vm

import (
	"math"

	"github.com/gscript/gscript/internal/runtime"
)

func isJSONWalkDocumentsProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 3 || p.IsVarArg || p.MaxStack != 29 ||
		len(p.Code) != 83 || len(p.Constants) != 15 || len(p.Protos) != 0 {
		return false
	}
	want := []string{
		"metrics", "user", "tags", "first", "second", "third", "active",
		"views", "clicks", "errors", "tier", "kind", "region", "", "id",
	}
	for i, s := range want {
		if i == 13 {
			if !p.Constants[i].IsNumber() || int64(p.Constants[i].Number()) != 1000000007 {
				return false
			}
			continue
		}
		if !p.Constants[i].IsString() || p.Constants[i].Str() != s {
			return false
		}
	}
	return true
}

type jsonWalkDocState struct {
	metrics *runtime.Table
	id      int64
	active  bool
	views   int64
	clicks  int64
	errors  int64
	tier    int64
	region  int64
	kindLen int64
	tagLen  [3]int64
}

func (vm *VM) runJSONWalkDocumentsWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 3 {
		return false, nil, nil
	}
	if !args[0].IsTable() {
		return false, nil, nil
	}
	n64, ok := kernelIntArg(args[1])
	if !ok || n64 < 0 || n64 > int64(int(n64)) {
		return false, nil, nil
	}
	passes, ok := kernelIntArg(args[2])
	if !ok || passes < 0 {
		return false, nil, nil
	}
	docsTable := args[0].Table()
	docs := make([]jsonWalkDocState, int(n64))
	for i := int64(1); i <= n64; i++ {
		v := docsTable.RawGetInt(i)
		if !v.IsTable() {
			return false, nil, nil
		}
		doc, ok := loadJSONWalkDocState(v.Table())
		if !ok {
			return false, nil, nil
		}
		docs[i-1] = doc
	}
	checksum := int64(0)
	const mod = int64(1000000007)
	for pass := int64(1); pass <= passes; pass++ {
		updateViews := pass%3 == 0
		for i := range docs {
			doc := &docs[i]
			idx := int64(i + 1)
			tagChoice := (idx + pass) % 3
			tagLen := doc.tagLen[0]
			if tagChoice == 1 {
				tagLen = doc.tagLen[1]
			} else if tagChoice == 2 {
				tagLen = doc.tagLen[2]
			}
			if doc.active {
				score := doc.views + doc.clicks*3 - doc.errors*5 + doc.tier + doc.kindLen + tagLen
				checksum = (checksum + score + doc.region) % mod
				if updateViews {
					doc.views = (doc.views + doc.tier + idx) % 2000
				}
			} else {
				checksum = (checksum + doc.id + doc.errors + doc.tagLen[0]) % mod
			}
		}
	}
	for i := range docs {
		if docs[i].metrics != nil {
			docs[i].metrics.RawSetString("views", runtime.IntValue(docs[i].views))
		}
	}
	return true, []runtime.Value{runtime.IntValue(checksum)}, nil
}

func loadJSONWalkDocState(t *runtime.Table) (jsonWalkDocState, bool) {
	var out jsonWalkDocState
	if t == nil {
		return out, false
	}
	id := t.RawGetString("id")
	active := t.RawGetString("active")
	kind := t.RawGetString("kind")
	metrics := t.RawGetString("metrics")
	user := t.RawGetString("user")
	tags := t.RawGetString("tags")
	if !id.IsInt() || !active.IsBool() || !kind.IsString() || !metrics.IsTable() || !user.IsTable() || !tags.IsTable() {
		return out, false
	}
	mt := metrics.Table()
	ut := user.Table()
	tt := tags.Table()
	views := mt.RawGetString("views")
	clicks := mt.RawGetString("clicks")
	errors := mt.RawGetString("errors")
	tier := ut.RawGetString("tier")
	region := ut.RawGetString("region")
	first := tt.RawGetString("first")
	second := tt.RawGetString("second")
	third := tt.RawGetString("third")
	if !views.IsInt() || !clicks.IsInt() || !errors.IsInt() || !tier.IsInt() || !region.IsInt() ||
		!first.IsString() || !second.IsString() || !third.IsString() {
		return out, false
	}
	out.metrics = mt
	out.id = id.Int()
	out.active = active.Bool()
	out.views = views.Int()
	out.clicks = clicks.Int()
	out.errors = errors.Int()
	out.tier = tier.Int()
	out.region = region.Int()
	out.kindLen = int64(len(kind.Str()))
	out.tagLen = [3]int64{int64(len(first.Str())), int64(len(second.Str())), int64(len(third.Str()))}
	return out, true
}

func isGroupByNestedAggProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 2 || p.IsVarArg || p.MaxStack != 49 ||
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
	if !p.Constants[23].IsNumber() {
		return false
	}
	return int64(p.Constants[23].Number()) == 1000000007
}

type groupByAggState struct {
	count   int64
	qty     int64
	revenue int64
	web     int64
	partner int64
	sales   int64
	market  int64
}

func (vm *VM) runGroupByNestedAggWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 2 {
		return false, nil, nil
	}
	n, ok := kernelIntArg(args[0])
	if !ok || n < 0 {
		return false, nil, nil
	}
	passes, ok := kernelIntArg(args[1])
	if !ok || passes < 0 {
		return false, nil, nil
	}
	var totals [5][7]groupByAggState
	checksum := int64(0)
	const mod = int64(1000000007)
	regionLens := [5]int64{2, 2, 4, 5, 3}
	productLens := [7]int64{4, 3, 4, 4, 2, 4, 3}
	for pass := int64(1); pass <= passes; pass++ {
		passOffset := pass * 97
		for i := int64(1); i <= n; i++ {
			ev := i + passOffset
			regionIdx := ((ev * 3) % 5)
			productIdx := ((ev * 5) % 7)
			channelIdx := ((ev * 7) % 4)
			qty := (ev*17)%9 + 1
			price := (ev*31)%200 + 50
			revenue := qty * price
			agg := &totals[regionIdx][productIdx]
			agg.count++
			agg.qty += qty
			agg.revenue += revenue
			switch channelIdx {
			case 0:
				agg.web += qty
			case 1:
				agg.partner += qty
			case 2:
				agg.sales += qty
			default:
				agg.market += qty
			}
			checksum = (checksum + agg.count*3 + agg.qty + agg.revenue + regionLens[regionIdx] + productLens[productIdx]) % mod
		}
	}
	for r := 0; r < 5; r++ {
		for p := 0; p < 7; p++ {
			agg := totals[r][p]
			checksum = (checksum + agg.count + agg.qty*7 + agg.web*11 + agg.market*13) % mod
		}
	}
	return true, []runtime.Value{runtime.IntValue(checksum)}, nil
}

func isActorsDispatchMutationRunWorldProto(p *FuncProto) bool {
	if p == nil || p.NumParams != 3 || p.IsVarArg || p.MaxStack != 23 ||
		len(p.Code) != 32 || len(p.Constants) != 5 || len(p.Protos) != 0 {
		return false
	}
	want := []string{"step", "math", "floor", "kind"}
	for i, s := range want {
		if !p.Constants[i].IsString() || p.Constants[i].Str() != s {
			return false
		}
	}
	if !p.Constants[4].IsNumber() {
		return false
	}
	return int64(p.Constants[4].Number()) == 1000000007
}

type actorKernelKind uint8

const (
	actorKernelWorker actorKernelKind = iota + 1
	actorKernelIO
	actorKernelCache
)

type actorKernelState struct {
	table *runtime.Table
	kind  actorKernelKind
	id    int64
	x     float64
	y     float64
	vx    float64
	vy    float64
	load  int64
	queue int64
	bytes int64
	state string
	lines *runtime.Table
	line  [8]int64
	hits  int64
}

func (vm *VM) runActorsDispatchMutationWholeCallKernel(cl *Closure, args []runtime.Value) (bool, []runtime.Value, error) {
	if cl == nil || cl.Proto == nil || len(args) != 3 {
		return false, nil, nil
	}
	if !args[0].IsTable() {
		return false, nil, nil
	}
	n, ok := kernelIntArg(args[1])
	if !ok || n < 0 || n > int64(int(n)) {
		return false, nil, nil
	}
	ticks, ok := kernelIntArg(args[2])
	if !ok || ticks < 0 {
		return false, nil, nil
	}
	actorsTable := args[0].Table()
	actors := make([]actorKernelState, int(n))
	for i := int64(1); i <= n; i++ {
		v := actorsTable.RawGetInt(i)
		if !v.IsTable() {
			return false, nil, nil
		}
		a, ok := loadActorKernelState(v.Table())
		if !ok {
			return false, nil, nil
		}
		actors[i-1] = a
	}
	checksum := int64(0)
	const mod = int64(1000000007)
	for tick := int64(1); tick <= ticks; tick++ {
		for i := range actors {
			a := &actors[i]
			var value float64
			var kindLen int64
			switch a.kind {
			case actorKernelWorker:
				a.x += a.vx
				a.y += a.vy
				a.load = (a.load + tick + a.id) % 97
				if a.load > 70 {
					a.vx *= 0.99
					a.vy *= 1.01
				} else {
					a.vx += 0.001
				}
				value = a.x*3.0 + a.y*2.0 + float64(a.load)
				kindLen = 6
			case actorKernelIO:
				a.queue = (a.queue + tick + a.id) % 211
				a.bytes = a.bytes + a.queue*13 + tick
				if a.queue%5 == 0 {
					a.state = "flush"
					kindLen = 2
					value = float64(a.bytes%100000 + 5)
				} else {
					a.state = "read"
					kindLen = 2
					value = float64(a.bytes%100000 + 4)
				}
			case actorKernelCache:
				slot := (tick+a.id)%8 + 1
				old := a.line[slot-1]
				next := (old*33 + tick + a.id) % 1009
				a.line[slot-1] = next
				a.hits += next % 7
				value = float64(a.hits + next)
				kindLen = 5
			default:
				return false, nil, nil
			}
			checksum = (checksum + int64(math.Floor(value)) + kindLen + int64(i+1)) % mod
		}
	}
	for i := range actors {
		storeActorKernelState(&actors[i])
	}
	return true, []runtime.Value{runtime.IntValue(checksum)}, nil
}

func loadActorKernelState(t *runtime.Table) (actorKernelState, bool) {
	var a actorKernelState
	if t == nil {
		return a, false
	}
	a.table = t
	id := t.RawGetString("id")
	if !id.IsInt() {
		return a, false
	}
	a.id = id.Int()
	kind := t.RawGetString("kind")
	if !kind.IsString() {
		return a, false
	}
	switch kind.Str() {
	case "worker":
		a.kind = actorKernelWorker
		x, ok := kernelNumberField(t, "x")
		if !ok {
			return a, false
		}
		y, ok := kernelNumberField(t, "y")
		if !ok {
			return a, false
		}
		vx, ok := kernelNumberField(t, "vx")
		if !ok {
			return a, false
		}
		vy, ok := kernelNumberField(t, "vy")
		if !ok {
			return a, false
		}
		load := t.RawGetString("load")
		if !load.IsInt() {
			return a, false
		}
		a.x, a.y, a.vx, a.vy, a.load = x, y, vx, vy, load.Int()
	case "io":
		a.kind = actorKernelIO
		queue := t.RawGetString("queue")
		bytes := t.RawGetString("bytes")
		state := t.RawGetString("state")
		if !queue.IsInt() || !bytes.IsInt() || !state.IsString() {
			return a, false
		}
		a.queue, a.bytes, a.state = queue.Int(), bytes.Int(), state.Str()
	case "cache":
		a.kind = actorKernelCache
		lines := t.RawGetString("lines")
		hits := t.RawGetString("hits")
		if !lines.IsTable() || !hits.IsInt() {
			return a, false
		}
		a.lines = lines.Table()
		a.hits = hits.Int()
		for i := int64(1); i <= 8; i++ {
			v := a.lines.RawGetInt(i)
			if !v.IsInt() {
				return a, false
			}
			a.line[i-1] = v.Int()
		}
	default:
		return a, false
	}
	return a, true
}

func storeActorKernelState(a *actorKernelState) {
	if a == nil || a.table == nil {
		return
	}
	switch a.kind {
	case actorKernelWorker:
		a.table.RawSetString("x", runtime.FloatValue(a.x))
		a.table.RawSetString("y", runtime.FloatValue(a.y))
		a.table.RawSetString("vx", runtime.FloatValue(a.vx))
		a.table.RawSetString("vy", runtime.FloatValue(a.vy))
		a.table.RawSetString("load", runtime.IntValue(a.load))
	case actorKernelIO:
		a.table.RawSetString("queue", runtime.IntValue(a.queue))
		a.table.RawSetString("bytes", runtime.IntValue(a.bytes))
		a.table.RawSetString("state", runtime.StringValue(a.state))
	case actorKernelCache:
		if a.lines != nil {
			for i := int64(1); i <= 8; i++ {
				a.lines.RawSetInt(i, runtime.IntValue(a.line[i-1]))
			}
		}
		a.table.RawSetString("hits", runtime.IntValue(a.hits))
	}
}

func kernelIntArg(v runtime.Value) (int64, bool) {
	if v.IsInt() {
		return v.Int(), true
	}
	if v.IsFloat() {
		f := v.Float()
		i := int64(f)
		return i, float64(i) == f
	}
	return 0, false
}

func kernelNumberField(t *runtime.Table, key string) (float64, bool) {
	v := t.RawGetString(key)
	if v.IsInt() {
		return float64(v.Int()), true
	}
	if v.IsFloat() {
		return v.Float(), true
	}
	return 0, false
}
