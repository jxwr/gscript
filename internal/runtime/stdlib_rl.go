package runtime

import (
	"fmt"
	"image/color"

	rl "github.com/gen2brain/raylib-go/raylib"
)

// ---------------------------------------------------------------------------
// Color conversion helpers
// ---------------------------------------------------------------------------

func rlColorFromValue(v Value) color.RGBA {
	if v.IsTable() {
		t := v.Table()
		r := uint8(t.RawGet(StringValue("r")).Int())
		g := uint8(t.RawGet(StringValue("g")).Int())
		b := uint8(t.RawGet(StringValue("b")).Int())
		a := uint8(t.RawGet(StringValue("a")).Int())
		return color.RGBA{R: r, G: g, B: b, A: a}
	}
	return color.RGBA{R: 255, G: 255, B: 255, A: 255}
}

func rlColorToValue(c color.RGBA) Value {
	t := NewTable()
	t.RawSet(StringValue("r"), IntValue(int64(c.R)))
	t.RawSet(StringValue("g"), IntValue(int64(c.G)))
	t.RawSet(StringValue("b"), IntValue(int64(c.B)))
	t.RawSet(StringValue("a"), IntValue(int64(c.A)))
	return TableValue(t)
}

func rlMakeColorValue(r, g, b, a uint8) Value {
	t := NewTable()
	t.RawSet(StringValue("r"), IntValue(int64(r)))
	t.RawSet(StringValue("g"), IntValue(int64(g)))
	t.RawSet(StringValue("b"), IntValue(int64(b)))
	t.RawSet(StringValue("a"), IntValue(int64(a)))
	return TableValue(t)
}

// ---------------------------------------------------------------------------
// Texture storage
// ---------------------------------------------------------------------------

var rlTextures = map[int32]rl.Texture2D{}
var rlTextureCounter int32

func rlMakeTextureValue(tex rl.Texture2D) Value {
	rlTextureCounter++
	id := rlTextureCounter
	rlTextures[id] = tex
	t := NewTable()
	t.RawSet(StringValue("__texture_id"), IntValue(int64(id)))
	t.RawSet(StringValue("width"), IntValue(int64(tex.Width)))
	t.RawSet(StringValue("height"), IntValue(int64(tex.Height)))
	return TableValue(t)
}

func rlGetTexture(v Value) (rl.Texture2D, bool) {
	if !v.IsTable() {
		return rl.Texture2D{}, false
	}
	idv := v.Table().RawGet(StringValue("__texture_id"))
	if !idv.IsInt() {
		return rl.Texture2D{}, false
	}
	tex, ok := rlTextures[int32(idv.Int())]
	return tex, ok
}

// ---------------------------------------------------------------------------
// Font storage
// ---------------------------------------------------------------------------

var rlFonts = map[int32]rl.Font{}
var rlFontCounter int32

func rlMakeFontValue(f rl.Font) Value {
	rlFontCounter++
	id := rlFontCounter
	rlFonts[id] = f
	t := NewTable()
	t.RawSet(StringValue("__font_id"), IntValue(int64(id)))
	t.RawSet(StringValue("baseSize"), IntValue(int64(f.BaseSize)))
	return TableValue(t)
}

func rlGetFont(v Value) (rl.Font, bool) {
	if !v.IsTable() {
		return rl.Font{}, false
	}
	idv := v.Table().RawGet(StringValue("__font_id"))
	if !idv.IsInt() {
		return rl.Font{}, false
	}
	f, ok := rlFonts[int32(idv.Int())]
	return f, ok
}

// ---------------------------------------------------------------------------
// Sound storage
// ---------------------------------------------------------------------------

var rlSounds = map[int32]rl.Sound{}
var rlSoundCounter int32

func rlMakeSoundValue(s rl.Sound) Value {
	rlSoundCounter++
	id := rlSoundCounter
	rlSounds[id] = s
	t := NewTable()
	t.RawSet(StringValue("__sound_id"), IntValue(int64(id)))
	return TableValue(t)
}

func rlGetSound(v Value) (rl.Sound, bool) {
	if !v.IsTable() {
		return rl.Sound{}, false
	}
	idv := v.Table().RawGet(StringValue("__sound_id"))
	if !idv.IsInt() {
		return rl.Sound{}, false
	}
	s, ok := rlSounds[int32(idv.Int())]
	return s, ok
}

// ---------------------------------------------------------------------------
// Music storage
// ---------------------------------------------------------------------------

var rlMusics = map[int32]rl.Music{}
var rlMusicCounter int32

func rlMakeMusicValue(m rl.Music) Value {
	rlMusicCounter++
	id := rlMusicCounter
	rlMusics[id] = m
	t := NewTable()
	t.RawSet(StringValue("__music_id"), IntValue(int64(id)))
	return TableValue(t)
}

func rlGetMusic(v Value) (rl.Music, bool) {
	if !v.IsTable() {
		return rl.Music{}, false
	}
	idv := v.Table().RawGet(StringValue("__music_id"))
	if !idv.IsInt() {
		return rl.Music{}, false
	}
	m, ok := rlMusics[int32(idv.Int())]
	return m, ok
}

// ---------------------------------------------------------------------------
// Camera2D helper
// ---------------------------------------------------------------------------

func rlCamera2DFromValue(v Value) rl.Camera2D {
	if !v.IsTable() {
		return rl.Camera2D{Zoom: 1.0}
	}
	t := v.Table()
	return rl.Camera2D{
		Offset: rl.Vector2{
			X: float32(toFloat(t.RawGet(StringValue("offsetX")))),
			Y: float32(toFloat(t.RawGet(StringValue("offsetY")))),
		},
		Target: rl.Vector2{
			X: float32(toFloat(t.RawGet(StringValue("targetX")))),
			Y: float32(toFloat(t.RawGet(StringValue("targetY")))),
		},
		Rotation: float32(toFloat(t.RawGet(StringValue("rotation")))),
		Zoom:     float32(toFloat(t.RawGet(StringValue("zoom")))),
	}
}

// ---------------------------------------------------------------------------
// rlLib: exposed to GScript as the "rl" global
// ---------------------------------------------------------------------------

func rlLib(interp *Interpreter) *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "rl." + name,
			Fn:   fn,
		}))
	}

	// ===================================================================
	// Color constants
	// ===================================================================
	t.RawSet(StringValue("LIGHTGRAY"), rlMakeColorValue(200, 200, 200, 255))
	t.RawSet(StringValue("GRAY"), rlMakeColorValue(130, 130, 130, 255))
	t.RawSet(StringValue("DARKGRAY"), rlMakeColorValue(80, 80, 80, 255))
	t.RawSet(StringValue("YELLOW"), rlMakeColorValue(253, 249, 0, 255))
	t.RawSet(StringValue("GOLD"), rlMakeColorValue(255, 203, 0, 255))
	t.RawSet(StringValue("ORANGE"), rlMakeColorValue(255, 161, 0, 255))
	t.RawSet(StringValue("PINK"), rlMakeColorValue(255, 109, 194, 255))
	t.RawSet(StringValue("RED"), rlMakeColorValue(230, 41, 55, 255))
	t.RawSet(StringValue("MAROON"), rlMakeColorValue(190, 33, 55, 255))
	t.RawSet(StringValue("GREEN"), rlMakeColorValue(0, 228, 48, 255))
	t.RawSet(StringValue("LIME"), rlMakeColorValue(0, 158, 47, 255))
	t.RawSet(StringValue("DARKGREEN"), rlMakeColorValue(0, 117, 44, 255))
	t.RawSet(StringValue("SKYBLUE"), rlMakeColorValue(102, 191, 255, 255))
	t.RawSet(StringValue("BLUE"), rlMakeColorValue(0, 121, 241, 255))
	t.RawSet(StringValue("DARKBLUE"), rlMakeColorValue(0, 82, 172, 255))
	t.RawSet(StringValue("PURPLE"), rlMakeColorValue(200, 122, 255, 255))
	t.RawSet(StringValue("VIOLET"), rlMakeColorValue(135, 60, 190, 255))
	t.RawSet(StringValue("DARKPURPLE"), rlMakeColorValue(112, 31, 126, 255))
	t.RawSet(StringValue("BEIGE"), rlMakeColorValue(211, 176, 131, 255))
	t.RawSet(StringValue("BROWN"), rlMakeColorValue(127, 106, 79, 255))
	t.RawSet(StringValue("DARKBROWN"), rlMakeColorValue(76, 63, 47, 255))
	t.RawSet(StringValue("WHITE"), rlMakeColorValue(255, 255, 255, 255))
	t.RawSet(StringValue("BLACK"), rlMakeColorValue(0, 0, 0, 255))
	t.RawSet(StringValue("BLANK"), rlMakeColorValue(0, 0, 0, 0))
	t.RawSet(StringValue("MAGENTA"), rlMakeColorValue(255, 0, 255, 255))
	t.RawSet(StringValue("RAYWHITE"), rlMakeColorValue(245, 245, 245, 255))

	// ===================================================================
	// Key constants
	// ===================================================================
	t.RawSet(StringValue("KEY_NULL"), IntValue(rl.KeyNull))
	t.RawSet(StringValue("KEY_APOSTROPHE"), IntValue(rl.KeyApostrophe))
	t.RawSet(StringValue("KEY_COMMA"), IntValue(rl.KeyComma))
	t.RawSet(StringValue("KEY_MINUS"), IntValue(rl.KeyMinus))
	t.RawSet(StringValue("KEY_PERIOD"), IntValue(rl.KeyPeriod))
	t.RawSet(StringValue("KEY_SLASH"), IntValue(rl.KeySlash))
	t.RawSet(StringValue("KEY_ZERO"), IntValue(rl.KeyZero))
	t.RawSet(StringValue("KEY_ONE"), IntValue(rl.KeyOne))
	t.RawSet(StringValue("KEY_TWO"), IntValue(rl.KeyTwo))
	t.RawSet(StringValue("KEY_THREE"), IntValue(rl.KeyThree))
	t.RawSet(StringValue("KEY_FOUR"), IntValue(rl.KeyFour))
	t.RawSet(StringValue("KEY_FIVE"), IntValue(rl.KeyFive))
	t.RawSet(StringValue("KEY_SIX"), IntValue(rl.KeySix))
	t.RawSet(StringValue("KEY_SEVEN"), IntValue(rl.KeySeven))
	t.RawSet(StringValue("KEY_EIGHT"), IntValue(rl.KeyEight))
	t.RawSet(StringValue("KEY_NINE"), IntValue(rl.KeyNine))
	t.RawSet(StringValue("KEY_SEMICOLON"), IntValue(rl.KeySemicolon))
	t.RawSet(StringValue("KEY_EQUAL"), IntValue(rl.KeyEqual))
	t.RawSet(StringValue("KEY_A"), IntValue(rl.KeyA))
	t.RawSet(StringValue("KEY_B"), IntValue(rl.KeyB))
	t.RawSet(StringValue("KEY_C"), IntValue(rl.KeyC))
	t.RawSet(StringValue("KEY_D"), IntValue(rl.KeyD))
	t.RawSet(StringValue("KEY_E"), IntValue(rl.KeyE))
	t.RawSet(StringValue("KEY_F"), IntValue(rl.KeyF))
	t.RawSet(StringValue("KEY_G"), IntValue(rl.KeyG))
	t.RawSet(StringValue("KEY_H"), IntValue(rl.KeyH))
	t.RawSet(StringValue("KEY_I"), IntValue(rl.KeyI))
	t.RawSet(StringValue("KEY_J"), IntValue(rl.KeyJ))
	t.RawSet(StringValue("KEY_K"), IntValue(rl.KeyK))
	t.RawSet(StringValue("KEY_L"), IntValue(rl.KeyL))
	t.RawSet(StringValue("KEY_M"), IntValue(rl.KeyM))
	t.RawSet(StringValue("KEY_N"), IntValue(rl.KeyN))
	t.RawSet(StringValue("KEY_O"), IntValue(rl.KeyO))
	t.RawSet(StringValue("KEY_P"), IntValue(rl.KeyP))
	t.RawSet(StringValue("KEY_Q"), IntValue(rl.KeyQ))
	t.RawSet(StringValue("KEY_R"), IntValue(rl.KeyR))
	t.RawSet(StringValue("KEY_S"), IntValue(rl.KeyS))
	t.RawSet(StringValue("KEY_T"), IntValue(rl.KeyT))
	t.RawSet(StringValue("KEY_U"), IntValue(rl.KeyU))
	t.RawSet(StringValue("KEY_V"), IntValue(rl.KeyV))
	t.RawSet(StringValue("KEY_W"), IntValue(rl.KeyW))
	t.RawSet(StringValue("KEY_X"), IntValue(rl.KeyX))
	t.RawSet(StringValue("KEY_Y"), IntValue(rl.KeyY))
	t.RawSet(StringValue("KEY_Z"), IntValue(rl.KeyZ))
	t.RawSet(StringValue("KEY_SPACE"), IntValue(rl.KeySpace))
	t.RawSet(StringValue("KEY_ESCAPE"), IntValue(rl.KeyEscape))
	t.RawSet(StringValue("KEY_ENTER"), IntValue(rl.KeyEnter))
	t.RawSet(StringValue("KEY_TAB"), IntValue(rl.KeyTab))
	t.RawSet(StringValue("KEY_BACKSPACE"), IntValue(rl.KeyBackspace))
	t.RawSet(StringValue("KEY_INSERT"), IntValue(rl.KeyInsert))
	t.RawSet(StringValue("KEY_DELETE"), IntValue(rl.KeyDelete))
	t.RawSet(StringValue("KEY_RIGHT"), IntValue(rl.KeyRight))
	t.RawSet(StringValue("KEY_LEFT"), IntValue(rl.KeyLeft))
	t.RawSet(StringValue("KEY_DOWN"), IntValue(rl.KeyDown))
	t.RawSet(StringValue("KEY_UP"), IntValue(rl.KeyUp))
	t.RawSet(StringValue("KEY_PAGE_UP"), IntValue(rl.KeyPageUp))
	t.RawSet(StringValue("KEY_PAGE_DOWN"), IntValue(rl.KeyPageDown))
	t.RawSet(StringValue("KEY_HOME"), IntValue(rl.KeyHome))
	t.RawSet(StringValue("KEY_END"), IntValue(rl.KeyEnd))
	t.RawSet(StringValue("KEY_F1"), IntValue(rl.KeyF1))
	t.RawSet(StringValue("KEY_F2"), IntValue(rl.KeyF2))
	t.RawSet(StringValue("KEY_F3"), IntValue(rl.KeyF3))
	t.RawSet(StringValue("KEY_F4"), IntValue(rl.KeyF4))
	t.RawSet(StringValue("KEY_F5"), IntValue(rl.KeyF5))
	t.RawSet(StringValue("KEY_F6"), IntValue(rl.KeyF6))
	t.RawSet(StringValue("KEY_F7"), IntValue(rl.KeyF7))
	t.RawSet(StringValue("KEY_F8"), IntValue(rl.KeyF8))
	t.RawSet(StringValue("KEY_F9"), IntValue(rl.KeyF9))
	t.RawSet(StringValue("KEY_F10"), IntValue(rl.KeyF10))
	t.RawSet(StringValue("KEY_F11"), IntValue(rl.KeyF11))
	t.RawSet(StringValue("KEY_F12"), IntValue(rl.KeyF12))
	t.RawSet(StringValue("KEY_LEFT_SHIFT"), IntValue(rl.KeyLeftShift))
	t.RawSet(StringValue("KEY_LEFT_CONTROL"), IntValue(rl.KeyLeftControl))
	t.RawSet(StringValue("KEY_LEFT_ALT"), IntValue(rl.KeyLeftAlt))
	t.RawSet(StringValue("KEY_RIGHT_SHIFT"), IntValue(rl.KeyRightShift))
	t.RawSet(StringValue("KEY_RIGHT_CONTROL"), IntValue(rl.KeyRightControl))
	t.RawSet(StringValue("KEY_RIGHT_ALT"), IntValue(rl.KeyRightAlt))

	// Mouse button constants
	t.RawSet(StringValue("MOUSE_BUTTON_LEFT"), IntValue(int64(rl.MouseButtonLeft)))
	t.RawSet(StringValue("MOUSE_BUTTON_RIGHT"), IntValue(int64(rl.MouseButtonRight)))
	t.RawSet(StringValue("MOUSE_BUTTON_MIDDLE"), IntValue(int64(rl.MouseButtonMiddle)))

	// ===================================================================
	// Window & core
	// ===================================================================

	// rl.initWindow(width, height, title)
	set("initWindow", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("rl.initWindow requires (width, height, title)")
		}
		rl.InitWindow(int32(toInt(args[0])), int32(toInt(args[1])), args[2].Str())
		return nil, nil
	})

	// rl.closeWindow()
	set("closeWindow", func(args []Value) ([]Value, error) {
		rl.CloseWindow()
		return nil, nil
	})

	// rl.windowShouldClose() -> bool
	set("windowShouldClose", func(args []Value) ([]Value, error) {
		return []Value{BoolValue(rl.WindowShouldClose())}, nil
	})

	// rl.setTargetFPS(fps)
	set("setTargetFPS", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.setTargetFPS requires fps")
		}
		rl.SetTargetFPS(int32(toInt(args[0])))
		return nil, nil
	})

	// rl.getFPS() -> int
	set("getFPS", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(rl.GetFPS()))}, nil
	})

	// rl.getFrameTime() -> float
	set("getFrameTime", func(args []Value) ([]Value, error) {
		return []Value{FloatValue(float64(rl.GetFrameTime()))}, nil
	})

	// rl.getTime() -> float
	set("getTime", func(args []Value) ([]Value, error) {
		return []Value{FloatValue(rl.GetTime())}, nil
	})

	// rl.setWindowTitle(title)
	set("setWindowTitle", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.setWindowTitle requires title")
		}
		rl.SetWindowTitle(args[0].Str())
		return nil, nil
	})

	// rl.setWindowSize(w, h)
	set("setWindowSize", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.setWindowSize requires (w, h)")
		}
		rl.SetWindowSize(int(toInt(args[0])), int(toInt(args[1])))
		return nil, nil
	})

	// rl.getScreenWidth() -> int
	set("getScreenWidth", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(rl.GetScreenWidth()))}, nil
	})

	// rl.getScreenHeight() -> int
	set("getScreenHeight", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(rl.GetScreenHeight()))}, nil
	})

	// rl.isWindowReady() -> bool
	set("isWindowReady", func(args []Value) ([]Value, error) {
		return []Value{BoolValue(rl.IsWindowReady())}, nil
	})

	// rl.isWindowMinimized() -> bool
	set("isWindowMinimized", func(args []Value) ([]Value, error) {
		return []Value{BoolValue(rl.IsWindowMinimized())}, nil
	})

	// rl.isWindowFocused() -> bool
	set("isWindowFocused", func(args []Value) ([]Value, error) {
		return []Value{BoolValue(rl.IsWindowFocused())}, nil
	})

	// rl.setWindowPosition(x, y)
	set("setWindowPosition", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.setWindowPosition requires (x, y)")
		}
		rl.SetWindowPosition(int(toInt(args[0])), int(toInt(args[1])))
		return nil, nil
	})

	// rl.toggleFullscreen()
	set("toggleFullscreen", func(args []Value) ([]Value, error) {
		rl.ToggleFullscreen()
		return nil, nil
	})

	// ===================================================================
	// Drawing
	// ===================================================================

	// rl.beginDrawing()
	set("beginDrawing", func(args []Value) ([]Value, error) {
		rl.BeginDrawing()
		return nil, nil
	})

	// rl.endDrawing()
	set("endDrawing", func(args []Value) ([]Value, error) {
		rl.EndDrawing()
		return nil, nil
	})

	// rl.clearBackground(color)
	set("clearBackground", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.clearBackground requires color")
		}
		rl.ClearBackground(rlColorFromValue(args[0]))
		return nil, nil
	})

	// rl.beginMode2D(camera)
	set("beginMode2D", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.beginMode2D requires camera")
		}
		rl.BeginMode2D(rlCamera2DFromValue(args[0]))
		return nil, nil
	})

	// rl.endMode2D()
	set("endMode2D", func(args []Value) ([]Value, error) {
		rl.EndMode2D()
		return nil, nil
	})

	// rl.beginScissorMode(x, y, w, h)
	set("beginScissorMode", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("rl.beginScissorMode requires (x, y, w, h)")
		}
		rl.BeginScissorMode(int32(toInt(args[0])), int32(toInt(args[1])),
			int32(toInt(args[2])), int32(toInt(args[3])))
		return nil, nil
	})

	// rl.endScissorMode()
	set("endScissorMode", func(args []Value) ([]Value, error) {
		rl.EndScissorMode()
		return nil, nil
	})

	// ===================================================================
	// Shapes
	// ===================================================================

	// rl.drawPixel(x, y, color)
	set("drawPixel", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("rl.drawPixel requires (x, y, color)")
		}
		rl.DrawPixel(int32(toInt(args[0])), int32(toInt(args[1])), rlColorFromValue(args[2]))
		return nil, nil
	})

	// rl.drawLine(x1, y1, x2, y2, color)
	set("drawLine", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.drawLine requires (x1, y1, x2, y2, color)")
		}
		rl.DrawLine(int32(toInt(args[0])), int32(toInt(args[1])),
			int32(toInt(args[2])), int32(toInt(args[3])),
			rlColorFromValue(args[4]))
		return nil, nil
	})

	// rl.drawLineEx(x1, y1, x2, y2, thick, color)
	set("drawLineEx", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.drawLineEx requires (x1, y1, x2, y2, thick, color)")
		}
		rl.DrawLineEx(
			rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))},
			rl.Vector2{X: float32(toFloat(args[2])), Y: float32(toFloat(args[3]))},
			float32(toFloat(args[4])),
			rlColorFromValue(args[5]),
		)
		return nil, nil
	})

	// rl.drawCircle(cx, cy, radius, color)
	set("drawCircle", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("rl.drawCircle requires (cx, cy, radius, color)")
		}
		rl.DrawCircle(int32(toInt(args[0])), int32(toInt(args[1])),
			float32(toFloat(args[2])), rlColorFromValue(args[3]))
		return nil, nil
	})

	// rl.drawCircleLines(cx, cy, radius, color)
	set("drawCircleLines", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("rl.drawCircleLines requires (cx, cy, radius, color)")
		}
		rl.DrawCircleLines(int32(toInt(args[0])), int32(toInt(args[1])),
			float32(toFloat(args[2])), rlColorFromValue(args[3]))
		return nil, nil
	})

	// rl.drawCircleGradient(cx, cy, radius, color1, color2)
	set("drawCircleGradient", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.drawCircleGradient requires (cx, cy, radius, color1, color2)")
		}
		rl.DrawCircleGradient(int32(toInt(args[0])), int32(toInt(args[1])),
			float32(toFloat(args[2])), rlColorFromValue(args[3]), rlColorFromValue(args[4]))
		return nil, nil
	})

	// rl.drawEllipse(cx, cy, rx, ry, color)
	set("drawEllipse", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.drawEllipse requires (cx, cy, rx, ry, color)")
		}
		rl.DrawEllipse(int32(toInt(args[0])), int32(toInt(args[1])),
			float32(toFloat(args[2])), float32(toFloat(args[3])),
			rlColorFromValue(args[4]))
		return nil, nil
	})

	// rl.drawRectangle(x, y, w, h, color)
	set("drawRectangle", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.drawRectangle requires (x, y, w, h, color)")
		}
		rl.DrawRectangle(int32(toInt(args[0])), int32(toInt(args[1])),
			int32(toInt(args[2])), int32(toInt(args[3])),
			rlColorFromValue(args[4]))
		return nil, nil
	})

	// rl.drawRectangleLines(x, y, w, h, color)
	set("drawRectangleLines", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.drawRectangleLines requires (x, y, w, h, color)")
		}
		rl.DrawRectangleLines(int32(toInt(args[0])), int32(toInt(args[1])),
			int32(toInt(args[2])), int32(toInt(args[3])),
			rlColorFromValue(args[4]))
		return nil, nil
	})

	// rl.drawRectangleLinesEx(x, y, w, h, lineThick, color)
	set("drawRectangleLinesEx", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.drawRectangleLinesEx requires (x, y, w, h, lineThick, color)")
		}
		rec := rl.Rectangle{
			X:      float32(toFloat(args[0])),
			Y:      float32(toFloat(args[1])),
			Width:  float32(toFloat(args[2])),
			Height: float32(toFloat(args[3])),
		}
		rl.DrawRectangleLinesEx(rec, float32(toFloat(args[4])), rlColorFromValue(args[5]))
		return nil, nil
	})

	// rl.drawRectangleRounded(x, y, w, h, roundness, segments, color)
	set("drawRectangleRounded", func(args []Value) ([]Value, error) {
		if len(args) < 7 {
			return nil, fmt.Errorf("rl.drawRectangleRounded requires (x, y, w, h, roundness, segments, color)")
		}
		rec := rl.Rectangle{
			X:      float32(toFloat(args[0])),
			Y:      float32(toFloat(args[1])),
			Width:  float32(toFloat(args[2])),
			Height: float32(toFloat(args[3])),
		}
		rl.DrawRectangleRounded(rec, float32(toFloat(args[4])), int32(toInt(args[5])),
			rlColorFromValue(args[6]))
		return nil, nil
	})

	// rl.drawRectangleGradientH(x, y, w, h, color1, color2)
	set("drawRectangleGradientH", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.drawRectangleGradientH requires (x, y, w, h, color1, color2)")
		}
		rl.DrawRectangleGradientH(int32(toInt(args[0])), int32(toInt(args[1])),
			int32(toInt(args[2])), int32(toInt(args[3])),
			rlColorFromValue(args[4]), rlColorFromValue(args[5]))
		return nil, nil
	})

	// rl.drawRectangleGradientV(x, y, w, h, color1, color2)
	set("drawRectangleGradientV", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.drawRectangleGradientV requires (x, y, w, h, color1, color2)")
		}
		rl.DrawRectangleGradientV(int32(toInt(args[0])), int32(toInt(args[1])),
			int32(toInt(args[2])), int32(toInt(args[3])),
			rlColorFromValue(args[4]), rlColorFromValue(args[5]))
		return nil, nil
	})

	// rl.drawTriangle(v1x, v1y, v2x, v2y, v3x, v3y, color)
	set("drawTriangle", func(args []Value) ([]Value, error) {
		if len(args) < 7 {
			return nil, fmt.Errorf("rl.drawTriangle requires (v1x, v1y, v2x, v2y, v3x, v3y, color)")
		}
		rl.DrawTriangle(
			rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))},
			rl.Vector2{X: float32(toFloat(args[2])), Y: float32(toFloat(args[3]))},
			rl.Vector2{X: float32(toFloat(args[4])), Y: float32(toFloat(args[5]))},
			rlColorFromValue(args[6]),
		)
		return nil, nil
	})

	// rl.drawTriangleLines(v1x, v1y, v2x, v2y, v3x, v3y, color)
	set("drawTriangleLines", func(args []Value) ([]Value, error) {
		if len(args) < 7 {
			return nil, fmt.Errorf("rl.drawTriangleLines requires (v1x, v1y, v2x, v2y, v3x, v3y, color)")
		}
		rl.DrawTriangleLines(
			rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))},
			rl.Vector2{X: float32(toFloat(args[2])), Y: float32(toFloat(args[3]))},
			rl.Vector2{X: float32(toFloat(args[4])), Y: float32(toFloat(args[5]))},
			rlColorFromValue(args[6]),
		)
		return nil, nil
	})

	// rl.drawPoly(cx, cy, sides, radius, rotation, color)
	set("drawPoly", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.drawPoly requires (cx, cy, sides, radius, rotation, color)")
		}
		rl.DrawPoly(
			rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))},
			int32(toInt(args[2])),
			float32(toFloat(args[3])),
			float32(toFloat(args[4])),
			rlColorFromValue(args[5]),
		)
		return nil, nil
	})

	// rl.drawPolyLines(cx, cy, sides, radius, rotation, color)
	set("drawPolyLines", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.drawPolyLines requires (cx, cy, sides, radius, rotation, color)")
		}
		rl.DrawPolyLines(
			rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))},
			int32(toInt(args[2])),
			float32(toFloat(args[3])),
			float32(toFloat(args[4])),
			rlColorFromValue(args[5]),
		)
		return nil, nil
	})

	// rl.drawRing(cx, cy, innerR, outerR, start, end, segs, color)
	set("drawRing", func(args []Value) ([]Value, error) {
		if len(args) < 8 {
			return nil, fmt.Errorf("rl.drawRing requires (cx, cy, innerR, outerR, start, end, segs, color)")
		}
		rl.DrawRing(
			rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))},
			float32(toFloat(args[2])),
			float32(toFloat(args[3])),
			float32(toFloat(args[4])),
			float32(toFloat(args[5])),
			int32(toInt(args[6])),
			rlColorFromValue(args[7]),
		)
		return nil, nil
	})

	// ===================================================================
	// Text
	// ===================================================================

	// rl.drawText(text, x, y, fontSize, color)
	set("drawText", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.drawText requires (text, x, y, fontSize, color)")
		}
		rl.DrawText(args[0].Str(), int32(toInt(args[1])), int32(toInt(args[2])),
			int32(toInt(args[3])), rlColorFromValue(args[4]))
		return nil, nil
	})

	// rl.drawTextEx(font, text, x, y, fontSize, spacing, color)
	set("drawTextEx", func(args []Value) ([]Value, error) {
		if len(args) < 7 {
			return nil, fmt.Errorf("rl.drawTextEx requires (font, text, x, y, fontSize, spacing, color)")
		}
		f, ok := rlGetFont(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.drawTextEx: invalid font")
		}
		pos := rl.Vector2{X: float32(toFloat(args[2])), Y: float32(toFloat(args[3]))}
		rl.DrawTextEx(f, args[1].Str(), pos,
			float32(toFloat(args[4])), float32(toFloat(args[5])),
			rlColorFromValue(args[6]))
		return nil, nil
	})

	// rl.measureText(text, fontSize) -> int
	set("measureText", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.measureText requires (text, fontSize)")
		}
		w := rl.MeasureText(args[0].Str(), int32(toInt(args[1])))
		return []Value{IntValue(int64(w))}, nil
	})

	// rl.measureTextEx(font, text, fontSize, spacing) -> width, height
	set("measureTextEx", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("rl.measureTextEx requires (font, text, fontSize, spacing)")
		}
		f, ok := rlGetFont(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.measureTextEx: invalid font")
		}
		v := rl.MeasureTextEx(f, args[1].Str(),
			float32(toFloat(args[2])), float32(toFloat(args[3])))
		return []Value{FloatValue(float64(v.X)), FloatValue(float64(v.Y))}, nil
	})

	// rl.loadFont(path) -> font table
	set("loadFont", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.loadFont requires path")
		}
		f := rl.LoadFont(args[0].Str())
		return []Value{rlMakeFontValue(f)}, nil
	})

	// rl.unloadFont(font)
	set("unloadFont", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.unloadFont requires font")
		}
		f, ok := rlGetFont(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.unloadFont: invalid font")
		}
		rl.UnloadFont(f)
		// Remove from map
		idv := args[0].Table().RawGet(StringValue("__font_id"))
		delete(rlFonts, int32(idv.Int()))
		return nil, nil
	})

	// rl.getFontDefault() -> font table
	set("getFontDefault", func(args []Value) ([]Value, error) {
		f := rl.GetFontDefault()
		return []Value{rlMakeFontValue(f)}, nil
	})

	// ===================================================================
	// Textures / Images
	// ===================================================================

	// rl.loadTexture(path) -> texture table
	set("loadTexture", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.loadTexture requires path")
		}
		tex := rl.LoadTexture(args[0].Str())
		return []Value{rlMakeTextureValue(tex)}, nil
	})

	// rl.unloadTexture(texture)
	set("unloadTexture", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.unloadTexture requires texture")
		}
		tex, ok := rlGetTexture(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.unloadTexture: invalid texture")
		}
		rl.UnloadTexture(tex)
		idv := args[0].Table().RawGet(StringValue("__texture_id"))
		delete(rlTextures, int32(idv.Int()))
		return nil, nil
	})

	// rl.drawTexture(texture, x, y, color)
	set("drawTexture", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("rl.drawTexture requires (texture, x, y, color)")
		}
		tex, ok := rlGetTexture(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.drawTexture: invalid texture")
		}
		rl.DrawTexture(tex, int32(toInt(args[1])), int32(toInt(args[2])),
			rlColorFromValue(args[3]))
		return nil, nil
	})

	// rl.drawTextureEx(texture, x, y, rotation, scale, color)
	set("drawTextureEx", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.drawTextureEx requires (texture, x, y, rotation, scale, color)")
		}
		tex, ok := rlGetTexture(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.drawTextureEx: invalid texture")
		}
		pos := rl.Vector2{X: float32(toFloat(args[1])), Y: float32(toFloat(args[2]))}
		rl.DrawTextureEx(tex, pos, float32(toFloat(args[3])),
			float32(toFloat(args[4])), rlColorFromValue(args[5]))
		return nil, nil
	})

	// rl.drawTextureRec(texture, srcX, srcY, srcW, srcH, dstX, dstY, color)
	set("drawTextureRec", func(args []Value) ([]Value, error) {
		if len(args) < 8 {
			return nil, fmt.Errorf("rl.drawTextureRec requires (texture, srcX, srcY, srcW, srcH, dstX, dstY, color)")
		}
		tex, ok := rlGetTexture(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.drawTextureRec: invalid texture")
		}
		src := rl.Rectangle{
			X:      float32(toFloat(args[1])),
			Y:      float32(toFloat(args[2])),
			Width:  float32(toFloat(args[3])),
			Height: float32(toFloat(args[4])),
		}
		pos := rl.Vector2{X: float32(toFloat(args[5])), Y: float32(toFloat(args[6]))}
		rl.DrawTextureRec(tex, src, pos, rlColorFromValue(args[7]))
		return nil, nil
	})

	// rl.drawTexturePro(texture, srcX, srcY, srcW, srcH, dstX, dstY, dstW, dstH, originX, originY, rotation, color)
	set("drawTexturePro", func(args []Value) ([]Value, error) {
		if len(args) < 13 {
			return nil, fmt.Errorf("rl.drawTexturePro requires 13 args")
		}
		tex, ok := rlGetTexture(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.drawTexturePro: invalid texture")
		}
		src := rl.Rectangle{
			X:      float32(toFloat(args[1])),
			Y:      float32(toFloat(args[2])),
			Width:  float32(toFloat(args[3])),
			Height: float32(toFloat(args[4])),
		}
		dst := rl.Rectangle{
			X:      float32(toFloat(args[5])),
			Y:      float32(toFloat(args[6])),
			Width:  float32(toFloat(args[7])),
			Height: float32(toFloat(args[8])),
		}
		origin := rl.Vector2{X: float32(toFloat(args[9])), Y: float32(toFloat(args[10]))}
		rl.DrawTexturePro(tex, src, dst, origin, float32(toFloat(args[11])),
			rlColorFromValue(args[12]))
		return nil, nil
	})

	// rl.genTextureMipmaps(texture) -> texture (updated)
	set("genTextureMipmaps", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.genTextureMipmaps requires texture")
		}
		tex, ok := rlGetTexture(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.genTextureMipmaps: invalid texture")
		}
		rl.GenTextureMipmaps(&tex)
		// Update the stored texture
		idv := args[0].Table().RawGet(StringValue("__texture_id"))
		rlTextures[int32(idv.Int())] = tex
		return []Value{args[0]}, nil
	})

	// ===================================================================
	// Keyboard input
	// ===================================================================

	// rl.isKeyDown(key) -> bool
	set("isKeyDown", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsKeyDown(int32(toInt(args[0]))))}, nil
	})

	// rl.isKeyUp(key) -> bool
	set("isKeyUp", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(true)}, nil
		}
		return []Value{BoolValue(rl.IsKeyUp(int32(toInt(args[0]))))}, nil
	})

	// rl.isKeyPressed(key) -> bool
	set("isKeyPressed", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsKeyPressed(int32(toInt(args[0]))))}, nil
	})

	// rl.isKeyReleased(key) -> bool
	set("isKeyReleased", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsKeyReleased(int32(toInt(args[0]))))}, nil
	})

	// rl.getKeyPressed() -> int
	set("getKeyPressed", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(rl.GetKeyPressed()))}, nil
	})

	// rl.setExitKey(key)
	set("setExitKey", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.setExitKey requires key")
		}
		rl.SetExitKey(int32(toInt(args[0])))
		return nil, nil
	})

	// ===================================================================
	// Mouse input
	// ===================================================================

	// rl.isMouseButtonDown(button) -> bool
	set("isMouseButtonDown", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsMouseButtonDown(rl.MouseButton(toInt(args[0]))))}, nil
	})

	// rl.isMouseButtonPressed(button) -> bool
	set("isMouseButtonPressed", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsMouseButtonPressed(rl.MouseButton(toInt(args[0]))))}, nil
	})

	// rl.isMouseButtonReleased(button) -> bool
	set("isMouseButtonReleased", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsMouseButtonReleased(rl.MouseButton(toInt(args[0]))))}, nil
	})

	// rl.getMouseX() -> int
	set("getMouseX", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(rl.GetMouseX()))}, nil
	})

	// rl.getMouseY() -> int
	set("getMouseY", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(rl.GetMouseY()))}, nil
	})

	// rl.getMousePosition() -> x, y
	set("getMousePosition", func(args []Value) ([]Value, error) {
		pos := rl.GetMousePosition()
		return []Value{IntValue(int64(pos.X)), IntValue(int64(pos.Y))}, nil
	})

	// rl.getMouseDelta() -> dx, dy
	set("getMouseDelta", func(args []Value) ([]Value, error) {
		d := rl.GetMouseDelta()
		return []Value{FloatValue(float64(d.X)), FloatValue(float64(d.Y))}, nil
	})

	// rl.getMouseWheelMove() -> float
	set("getMouseWheelMove", func(args []Value) ([]Value, error) {
		return []Value{FloatValue(float64(rl.GetMouseWheelMove()))}, nil
	})

	// rl.setMousePosition(x, y)
	set("setMousePosition", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.setMousePosition requires (x, y)")
		}
		rl.SetMousePosition(int(toInt(args[0])), int(toInt(args[1])))
		return nil, nil
	})

	// rl.setMouseCursor(cursor)
	set("setMouseCursor", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.setMouseCursor requires cursor")
		}
		rl.SetMouseCursor(int32(toInt(args[0])))
		return nil, nil
	})

	// rl.showCursor()
	set("showCursor", func(args []Value) ([]Value, error) {
		rl.ShowCursor()
		return nil, nil
	})

	// rl.hideCursor()
	set("hideCursor", func(args []Value) ([]Value, error) {
		rl.HideCursor()
		return nil, nil
	})

	// rl.isCursorHidden() -> bool
	set("isCursorHidden", func(args []Value) ([]Value, error) {
		return []Value{BoolValue(rl.IsCursorHidden())}, nil
	})

	// ===================================================================
	// Audio
	// ===================================================================

	// rl.initAudioDevice()
	set("initAudioDevice", func(args []Value) ([]Value, error) {
		rl.InitAudioDevice()
		return nil, nil
	})

	// rl.closeAudioDevice()
	set("closeAudioDevice", func(args []Value) ([]Value, error) {
		rl.CloseAudioDevice()
		return nil, nil
	})

	// rl.isAudioDeviceReady() -> bool
	set("isAudioDeviceReady", func(args []Value) ([]Value, error) {
		return []Value{BoolValue(rl.IsAudioDeviceReady())}, nil
	})

	// rl.setMasterVolume(vol)
	set("setMasterVolume", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.setMasterVolume requires vol")
		}
		rl.SetMasterVolume(float32(toFloat(args[0])))
		return nil, nil
	})

	// rl.loadSound(path) -> sound table
	set("loadSound", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.loadSound requires path")
		}
		s := rl.LoadSound(args[0].Str())
		return []Value{rlMakeSoundValue(s)}, nil
	})

	// rl.unloadSound(sound)
	set("unloadSound", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.unloadSound requires sound")
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.unloadSound: invalid sound")
		}
		rl.UnloadSound(s)
		idv := args[0].Table().RawGet(StringValue("__sound_id"))
		delete(rlSounds, int32(idv.Int()))
		return nil, nil
	})

	// rl.playSound(sound)
	set("playSound", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.playSound requires sound")
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.playSound: invalid sound")
		}
		rl.PlaySound(s)
		return nil, nil
	})

	// rl.stopSound(sound)
	set("stopSound", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.stopSound requires sound")
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.stopSound: invalid sound")
		}
		rl.StopSound(s)
		return nil, nil
	})

	// rl.pauseSound(sound)
	set("pauseSound", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.pauseSound requires sound")
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.pauseSound: invalid sound")
		}
		rl.PauseSound(s)
		return nil, nil
	})

	// rl.resumeSound(sound)
	set("resumeSound", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.resumeSound requires sound")
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.resumeSound: invalid sound")
		}
		rl.ResumeSound(s)
		return nil, nil
	})

	// rl.isSoundPlaying(sound) -> bool
	set("isSoundPlaying", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsSoundPlaying(s))}, nil
	})

	// rl.setSoundVolume(sound, vol)
	set("setSoundVolume", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.setSoundVolume requires (sound, vol)")
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.setSoundVolume: invalid sound")
		}
		rl.SetSoundVolume(s, float32(toFloat(args[1])))
		return nil, nil
	})

	// rl.setSoundPitch(sound, pitch)
	set("setSoundPitch", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.setSoundPitch requires (sound, pitch)")
		}
		s, ok := rlGetSound(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.setSoundPitch: invalid sound")
		}
		rl.SetSoundPitch(s, float32(toFloat(args[1])))
		return nil, nil
	})

	// rl.loadMusicStream(path) -> music table
	set("loadMusicStream", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.loadMusicStream requires path")
		}
		m := rl.LoadMusicStream(args[0].Str())
		return []Value{rlMakeMusicValue(m)}, nil
	})

	// rl.unloadMusicStream(music)
	set("unloadMusicStream", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.unloadMusicStream requires music")
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.unloadMusicStream: invalid music")
		}
		rl.UnloadMusicStream(m)
		idv := args[0].Table().RawGet(StringValue("__music_id"))
		delete(rlMusics, int32(idv.Int()))
		return nil, nil
	})

	// rl.playMusicStream(music)
	set("playMusicStream", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.playMusicStream requires music")
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.playMusicStream: invalid music")
		}
		rl.PlayMusicStream(m)
		return nil, nil
	})

	// rl.stopMusicStream(music)
	set("stopMusicStream", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.stopMusicStream requires music")
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.stopMusicStream: invalid music")
		}
		rl.StopMusicStream(m)
		return nil, nil
	})

	// rl.pauseMusicStream(music)
	set("pauseMusicStream", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.pauseMusicStream requires music")
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.pauseMusicStream: invalid music")
		}
		rl.PauseMusicStream(m)
		return nil, nil
	})

	// rl.resumeMusicStream(music)
	set("resumeMusicStream", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.resumeMusicStream requires music")
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.resumeMusicStream: invalid music")
		}
		rl.ResumeMusicStream(m)
		return nil, nil
	})

	// rl.updateMusicStream(music)
	set("updateMusicStream", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.updateMusicStream requires music")
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.updateMusicStream: invalid music")
		}
		rl.UpdateMusicStream(m)
		return nil, nil
	})

	// rl.isMusicStreamPlaying(music) -> bool
	set("isMusicStreamPlaying", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return []Value{BoolValue(false)}, nil
		}
		return []Value{BoolValue(rl.IsMusicStreamPlaying(m))}, nil
	})

	// rl.setMusicVolume(music, vol)
	set("setMusicVolume", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.setMusicVolume requires (music, vol)")
		}
		m, ok := rlGetMusic(args[0])
		if !ok {
			return nil, fmt.Errorf("rl.setMusicVolume: invalid music")
		}
		rl.SetMusicVolume(m, float32(toFloat(args[1])))
		return nil, nil
	})

	// ===================================================================
	// Collision
	// ===================================================================

	// rl.checkCollisionRecs(x1,y1,w1,h1, x2,y2,w2,h2) -> bool
	set("checkCollisionRecs", func(args []Value) ([]Value, error) {
		if len(args) < 8 {
			return nil, fmt.Errorf("rl.checkCollisionRecs requires 8 args")
		}
		r1 := rl.Rectangle{
			X: float32(toFloat(args[0])), Y: float32(toFloat(args[1])),
			Width: float32(toFloat(args[2])), Height: float32(toFloat(args[3])),
		}
		r2 := rl.Rectangle{
			X: float32(toFloat(args[4])), Y: float32(toFloat(args[5])),
			Width: float32(toFloat(args[6])), Height: float32(toFloat(args[7])),
		}
		return []Value{BoolValue(rl.CheckCollisionRecs(r1, r2))}, nil
	})

	// rl.checkCollisionCircles(cx1,cy1,r1, cx2,cy2,r2) -> bool
	set("checkCollisionCircles", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.checkCollisionCircles requires 6 args")
		}
		c1 := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		c2 := rl.Vector2{X: float32(toFloat(args[3])), Y: float32(toFloat(args[4]))}
		return []Value{BoolValue(rl.CheckCollisionCircles(c1, float32(toFloat(args[2])),
			c2, float32(toFloat(args[5]))))}, nil
	})

	// rl.checkCollisionCircleRec(cx,cy,r, x,y,w,h) -> bool
	set("checkCollisionCircleRec", func(args []Value) ([]Value, error) {
		if len(args) < 7 {
			return nil, fmt.Errorf("rl.checkCollisionCircleRec requires 7 args")
		}
		center := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		rec := rl.Rectangle{
			X: float32(toFloat(args[3])), Y: float32(toFloat(args[4])),
			Width: float32(toFloat(args[5])), Height: float32(toFloat(args[6])),
		}
		return []Value{BoolValue(rl.CheckCollisionCircleRec(center, float32(toFloat(args[2])), rec))}, nil
	})

	// rl.checkCollisionPointRec(px,py, x,y,w,h) -> bool
	set("checkCollisionPointRec", func(args []Value) ([]Value, error) {
		if len(args) < 6 {
			return nil, fmt.Errorf("rl.checkCollisionPointRec requires 6 args")
		}
		pt := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		rec := rl.Rectangle{
			X: float32(toFloat(args[2])), Y: float32(toFloat(args[3])),
			Width: float32(toFloat(args[4])), Height: float32(toFloat(args[5])),
		}
		return []Value{BoolValue(rl.CheckCollisionPointRec(pt, rec))}, nil
	})

	// rl.checkCollisionPointCircle(px,py, cx,cy,r) -> bool
	set("checkCollisionPointCircle", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.checkCollisionPointCircle requires 5 args")
		}
		pt := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		center := rl.Vector2{X: float32(toFloat(args[2])), Y: float32(toFloat(args[3]))}
		return []Value{BoolValue(rl.CheckCollisionPointCircle(pt, center, float32(toFloat(args[4]))))}, nil
	})

	// rl.getCollisionRec(x1,y1,w1,h1, x2,y2,w2,h2) -> x,y,w,h
	set("getCollisionRec", func(args []Value) ([]Value, error) {
		if len(args) < 8 {
			return nil, fmt.Errorf("rl.getCollisionRec requires 8 args")
		}
		r1 := rl.Rectangle{
			X: float32(toFloat(args[0])), Y: float32(toFloat(args[1])),
			Width: float32(toFloat(args[2])), Height: float32(toFloat(args[3])),
		}
		r2 := rl.Rectangle{
			X: float32(toFloat(args[4])), Y: float32(toFloat(args[5])),
			Width: float32(toFloat(args[6])), Height: float32(toFloat(args[7])),
		}
		rec := rl.GetCollisionRec(r1, r2)
		return []Value{
			FloatValue(float64(rec.X)),
			FloatValue(float64(rec.Y)),
			FloatValue(float64(rec.Width)),
			FloatValue(float64(rec.Height)),
		}, nil
	})

	// ===================================================================
	// Math utilities
	// ===================================================================

	// rl.vector2Length(x, y) -> float
	set("vector2Length", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.vector2Length requires (x, y)")
		}
		v := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		return []Value{FloatValue(float64(rl.Vector2Length(v)))}, nil
	})

	// rl.vector2Normalize(x, y) -> nx, ny
	set("vector2Normalize", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.vector2Normalize requires (x, y)")
		}
		v := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		n := rl.Vector2Normalize(v)
		return []Value{FloatValue(float64(n.X)), FloatValue(float64(n.Y))}, nil
	})

	// rl.vector2Distance(x1,y1, x2,y2) -> float
	set("vector2Distance", func(args []Value) ([]Value, error) {
		if len(args) < 4 {
			return nil, fmt.Errorf("rl.vector2Distance requires (x1, y1, x2, y2)")
		}
		v1 := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		v2 := rl.Vector2{X: float32(toFloat(args[2])), Y: float32(toFloat(args[3]))}
		return []Value{FloatValue(float64(rl.Vector2Distance(v1, v2)))}, nil
	})

	// rl.vector2Lerp(x1,y1, x2,y2, t) -> rx, ry
	set("vector2Lerp", func(args []Value) ([]Value, error) {
		if len(args) < 5 {
			return nil, fmt.Errorf("rl.vector2Lerp requires (x1, y1, x2, y2, t)")
		}
		v1 := rl.Vector2{X: float32(toFloat(args[0])), Y: float32(toFloat(args[1]))}
		v2 := rl.Vector2{X: float32(toFloat(args[2])), Y: float32(toFloat(args[3]))}
		r := rl.Vector2Lerp(v1, v2, float32(toFloat(args[4])))
		return []Value{FloatValue(float64(r.X)), FloatValue(float64(r.Y))}, nil
	})

	// rl.fade(color, alpha) -> color
	set("fade", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.fade requires (color, alpha)")
		}
		c := rl.Fade(rlColorFromValue(args[0]), float32(toFloat(args[1])))
		return []Value{rlColorToValue(c)}, nil
	})

	// rl.colorAlpha(color, alpha) -> color
	set("colorAlpha", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.colorAlpha requires (color, alpha)")
		}
		c := rl.ColorAlpha(rlColorFromValue(args[0]), float32(toFloat(args[1])))
		return []Value{rlColorToValue(c)}, nil
	})

	// rl.colorBrightness(color, factor) -> color
	set("colorBrightness", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.colorBrightness requires (color, factor)")
		}
		c := rl.ColorBrightness(rlColorFromValue(args[0]), float32(toFloat(args[1])))
		return []Value{rlColorToValue(c)}, nil
	})

	// rl.colorContrast(color, contrast) -> color
	set("colorContrast", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.colorContrast requires (color, contrast)")
		}
		c := rl.ColorContrast(rlColorFromValue(args[0]), float32(toFloat(args[1])))
		return []Value{rlColorToValue(c)}, nil
	})

	// rl.colorToHSV(color) -> h, s, v
	set("colorToHSV", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("rl.colorToHSV requires color")
		}
		hsv := rl.ColorToHSV(rlColorFromValue(args[0]))
		return []Value{
			FloatValue(float64(hsv.X)),
			FloatValue(float64(hsv.Y)),
			FloatValue(float64(hsv.Z)),
		}, nil
	})

	// rl.colorFromHSV(h, s, v) -> color
	set("colorFromHSV", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("rl.colorFromHSV requires (h, s, v)")
		}
		c := rl.ColorFromHSV(float32(toFloat(args[0])),
			float32(toFloat(args[1])), float32(toFloat(args[2])))
		return []Value{rlColorToValue(c)}, nil
	})

	// rl.getRandomValue(min, max) -> int
	set("getRandomValue", func(args []Value) ([]Value, error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("rl.getRandomValue requires (min, max)")
		}
		v := rl.GetRandomValue(int32(toInt(args[0])), int32(toInt(args[1])))
		return []Value{IntValue(int64(v))}, nil
	})

	return t
}
