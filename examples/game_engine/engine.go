package main

import (
	"fmt"
	"math"
	"os"

	gs "github.com/gscript/gscript/gscript"
)

// ---- Game Engine Types ----

type Vec2 struct {
	X, Y float64
}

func (v Vec2) Length() float64      { return math.Sqrt(v.X*v.X + v.Y*v.Y) }
func (v Vec2) Add(o Vec2) Vec2     { return Vec2{v.X + o.X, v.Y + o.Y} }
func (v Vec2) Sub(o Vec2) Vec2     { return Vec2{v.X - o.X, v.Y - o.Y} }
func (v Vec2) Scale(f float64) Vec2 { return Vec2{v.X * f, v.Y * f} }
func (v Vec2) Normalize() Vec2 {
	l := v.Length()
	if l == 0 {
		return Vec2{}
	}
	return Vec2{v.X / l, v.Y / l}
}
func (v Vec2) Dot(o Vec2) float64      { return v.X*o.X + v.Y*o.Y }
func (v Vec2) Distance(o Vec2) float64 { return v.Sub(o).Length() }

// Entity is a simple game entity with a position.
type Entity struct {
	Name   string
	Active bool
	Pos    Vec2
}

func NewEntity(name string) *Entity {
	return &Entity{
		Name:   name,
		Active: true,
	}
}

func (e Entity) IsActive() bool { return e.Active }

// Input tracks key state.
type Input struct {
	keys map[string]bool
}

func NewInput() *Input { return &Input{keys: make(map[string]bool)} }
func (inp *Input) Press(key string)       { inp.keys[key] = true }
func (inp *Input) Release(key string)     { inp.keys[key] = false }
func (inp *Input) IsDown(key string) bool { return inp.keys[key] }

// ---- Engine ----

type Engine struct {
	vm       *gs.VM
	input    *Input
	entities map[string]*Entity
}

func NewEngine(scriptPath string) (*Engine, error) {
	e := &Engine{
		input:    NewInput(),
		entities: make(map[string]*Entity),
	}

	vm := gs.New(gs.WithLibs(gs.LibSafe | gs.LibMath))

	// Bind Vec2 type
	vm.BindStruct("Vec2", Vec2{})

	// Register engine API
	vm.RegisterTable("engine", map[string]interface{}{
		"createEntity": func(name string, x, y float64) {
			ent := NewEntity(name)
			ent.Pos = Vec2{x, y}
			e.entities[name] = ent
		},
		"getEntityPos": func(name string) Vec2 {
			ent, ok := e.entities[name]
			if !ok {
				return Vec2{}
			}
			return ent.Pos
		},
		"setEntityPos": func(name string, pos Vec2) {
			if ent, ok := e.entities[name]; ok {
				ent.Pos = pos
			}
		},
		"isEntityActive": func(name string) bool {
			ent, ok := e.entities[name]
			return ok && ent.Active
		},
		"deactivateEntity": func(name string) {
			if ent, ok := e.entities[name]; ok {
				ent.Active = false
			}
		},
		"entityCount": func() int { return len(e.entities) },
		"log":         func(msg string) { fmt.Println("[GScript]", msg) },
	})

	// Register input
	vm.RegisterTable("input", map[string]interface{}{
		"isDown":  func(key string) bool { return e.input.IsDown(key) },
		"press":   func(key string) { e.input.Press(key) },
		"release": func(key string) { e.input.Release(key) },
	})

	// Register vec2 helper functions
	vm.RegisterTable("vec2", map[string]interface{}{
		"zero":  func() Vec2 { return Vec2{} },
		"one":   func() Vec2 { return Vec2{1, 1} },
		"up":    func() Vec2 { return Vec2{0, -1} },
		"down":  func() Vec2 { return Vec2{0, 1} },
		"left":  func() Vec2 { return Vec2{-1, 0} },
		"right": func() Vec2 { return Vec2{1, 0} },
		"lerp": func(a, b Vec2, t float64) Vec2 {
			return Vec2{a.X + (b.X-a.X)*t, a.Y + (b.Y-a.Y)*t}
		},
	})

	vm.Set("dt", 0.016) // default delta time

	e.vm = vm

	if err := vm.ExecFile(scriptPath); err != nil {
		return nil, err
	}

	return e, nil
}

func (e *Engine) Start() error {
	_, err := e.vm.Call("onStart")
	return err
}

func (e *Engine) Update(dt float64) error {
	e.vm.Set("dt", dt)
	_, err := e.vm.Call("onUpdate", dt)
	return err
}

func (e *Engine) SimulateInput(key string, down bool) {
	if down {
		e.input.Press(key)
	} else {
		e.input.Release(key)
	}
}

func main() {
	scriptPath := "examples/game_engine/game.gs"
	if len(os.Args) > 1 {
		scriptPath = os.Args[1]
	}

	eng, err := NewEngine(scriptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Engine error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== GScript Game Engine Demo ===")

	if err := eng.Start(); err != nil {
		fmt.Println("onStart error:", err)
	}

	// Simulate 5 frames
	fmt.Println("\n--- Simulating 5 frames ---")
	for i := 0; i < 5; i++ {
		if i == 2 {
			eng.SimulateInput("LEFT", true)
		}
		if i == 4 {
			eng.SimulateInput("LEFT", false)
			eng.SimulateInput("SPACE", true)
		}
		if err := eng.Update(0.016); err != nil {
			fmt.Printf("Frame %d error: %v\n", i+1, err)
		}
	}

	fmt.Printf("\nTotal entities: %d\n", len(eng.entities))
}
