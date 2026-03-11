package runtime

import "testing"

// TestRLLibCompiles verifies the rl stdlib compiles and exports expected symbols.
func TestRLLibCompiles(t *testing.T) {
	// Call rlLib directly (nil interp is OK since no function uses it).
	lib := rlLib(nil)
	if lib == nil {
		t.Fatal("rlLib returned nil")
	}
	t.Log("rl lib compiled successfully")
}

func TestRLColorConstants(t *testing.T) {
	lib := rlLib(nil)

	// Check that RED constant exists and has correct value
	red := lib.RawGet(StringValue("RED"))
	if !red.IsTable() {
		t.Fatal("rl.RED should be a table")
	}
	r := red.Table().RawGet(StringValue("r"))
	if !r.IsInt() || r.Int() != 230 {
		t.Errorf("rl.RED.r should be 230, got %v", r)
	}
	g := red.Table().RawGet(StringValue("g"))
	if !g.IsInt() || g.Int() != 41 {
		t.Errorf("rl.RED.g should be 41, got %v", g)
	}
	b := red.Table().RawGet(StringValue("b"))
	if !b.IsInt() || b.Int() != 55 {
		t.Errorf("rl.RED.b should be 55, got %v", b)
	}
	a := red.Table().RawGet(StringValue("a"))
	if !a.IsInt() || a.Int() != 255 {
		t.Errorf("rl.RED.a should be 255, got %v", a)
	}
}

func TestRLKeyConstants(t *testing.T) {
	lib := rlLib(nil)

	// Verify a few key constants
	tests := []struct {
		name string
		want int64
	}{
		{"KEY_SPACE", 32},
		{"KEY_ESCAPE", 256},
		{"KEY_ENTER", 257},
		{"KEY_A", 65},
		{"KEY_Z", 90},
		{"KEY_ZERO", 48},
		{"KEY_NINE", 57},
		{"KEY_F1", 290},
		{"KEY_F12", 301},
		{"KEY_LEFT", 263},
		{"KEY_RIGHT", 262},
		{"KEY_UP", 265},
		{"KEY_DOWN", 264},
	}

	for _, tt := range tests {
		v := lib.RawGet(StringValue(tt.name))
		if !v.IsInt() {
			t.Errorf("rl.%s should be an int, got %v", tt.name, v.TypeName())
			continue
		}
		if v.Int() != tt.want {
			t.Errorf("rl.%s = %d, want %d", tt.name, v.Int(), tt.want)
		}
	}
}

func TestRLMouseButtonConstants(t *testing.T) {
	lib := rlLib(nil)

	left := lib.RawGet(StringValue("MOUSE_BUTTON_LEFT"))
	if !left.IsInt() || left.Int() != 0 {
		t.Errorf("rl.MOUSE_BUTTON_LEFT should be 0, got %v", left)
	}
	right := lib.RawGet(StringValue("MOUSE_BUTTON_RIGHT"))
	if !right.IsInt() || right.Int() != 1 {
		t.Errorf("rl.MOUSE_BUTTON_RIGHT should be 1, got %v", right)
	}
	middle := lib.RawGet(StringValue("MOUSE_BUTTON_MIDDLE"))
	if !middle.IsInt() || middle.Int() != 2 {
		t.Errorf("rl.MOUSE_BUTTON_MIDDLE should be 2, got %v", middle)
	}
}

func TestRLFunctionExports(t *testing.T) {
	lib := rlLib(nil)

	// Verify that key functions are exported
	funcs := []string{
		"initWindow", "closeWindow", "windowShouldClose",
		"setTargetFPS", "getFPS", "getFrameTime", "getTime",
		"beginDrawing", "endDrawing", "clearBackground",
		"drawText", "drawRectangle", "drawCircle", "drawLine",
		"loadTexture", "unloadTexture", "drawTexture",
		"isKeyDown", "isKeyPressed", "isKeyUp", "isKeyReleased",
		"isMouseButtonDown", "isMouseButtonPressed", "getMouseX", "getMouseY",
		"initAudioDevice", "loadSound", "playSound",
		"loadMusicStream", "playMusicStream", "updateMusicStream",
		"checkCollisionRecs", "checkCollisionCircles", "checkCollisionPointRec",
		"vector2Length", "vector2Normalize", "vector2Distance", "vector2Lerp",
		"fade", "colorAlpha", "colorToHSV", "colorFromHSV",
		"getRandomValue",
		"beginMode2D", "endMode2D",
		"drawTextureEx", "drawTextureRec", "drawTexturePro",
		"loadFont", "unloadFont", "drawTextEx", "measureText", "measureTextEx",
		"drawPixel", "drawLineEx", "drawCircleLines", "drawCircleGradient",
		"drawEllipse", "drawRectangleLines", "drawRectangleLinesEx",
		"drawRectangleRounded", "drawRectangleGradientH", "drawRectangleGradientV",
		"drawTriangle", "drawTriangleLines", "drawPoly", "drawPolyLines", "drawRing",
		"setWindowTitle", "setWindowSize", "getScreenWidth", "getScreenHeight",
		"toggleFullscreen",
		"showCursor", "hideCursor", "isCursorHidden",
		"setMousePosition", "setMouseCursor", "getMouseWheelMove", "getMouseDelta",
		"setMasterVolume", "setSoundVolume", "setSoundPitch",
		"setMusicVolume",
		"colorBrightness", "colorContrast",
		"getCollisionRec",
		"beginScissorMode", "endScissorMode",
	}

	for _, name := range funcs {
		v := lib.RawGet(StringValue(name))
		if !v.IsFunction() {
			t.Errorf("rl.%s should be a function, got %v", name, v.TypeName())
		}
	}
}

func TestRLColorAllConstants(t *testing.T) {
	lib := rlLib(nil)

	colors := []struct {
		name string
		r, g, b, a uint8
	}{
		{"LIGHTGRAY", 200, 200, 200, 255},
		{"GRAY", 130, 130, 130, 255},
		{"DARKGRAY", 80, 80, 80, 255},
		{"YELLOW", 253, 249, 0, 255},
		{"GOLD", 255, 203, 0, 255},
		{"ORANGE", 255, 161, 0, 255},
		{"PINK", 255, 109, 194, 255},
		{"RED", 230, 41, 55, 255},
		{"MAROON", 190, 33, 55, 255},
		{"GREEN", 0, 228, 48, 255},
		{"LIME", 0, 158, 47, 255},
		{"DARKGREEN", 0, 117, 44, 255},
		{"SKYBLUE", 102, 191, 255, 255},
		{"BLUE", 0, 121, 241, 255},
		{"DARKBLUE", 0, 82, 172, 255},
		{"PURPLE", 200, 122, 255, 255},
		{"VIOLET", 135, 60, 190, 255},
		{"DARKPURPLE", 112, 31, 126, 255},
		{"BEIGE", 211, 176, 131, 255},
		{"BROWN", 127, 106, 79, 255},
		{"DARKBROWN", 76, 63, 47, 255},
		{"WHITE", 255, 255, 255, 255},
		{"BLACK", 0, 0, 0, 255},
		{"BLANK", 0, 0, 0, 0},
		{"MAGENTA", 255, 0, 255, 255},
		{"RAYWHITE", 245, 245, 245, 255},
	}

	for _, c := range colors {
		v := lib.RawGet(StringValue(c.name))
		if !v.IsTable() {
			t.Errorf("rl.%s should be a table", c.name)
			continue
		}
		tbl := v.Table()
		r := tbl.RawGet(StringValue("r"))
		g := tbl.RawGet(StringValue("g"))
		b := tbl.RawGet(StringValue("b"))
		a := tbl.RawGet(StringValue("a"))
		if r.Int() != int64(c.r) || g.Int() != int64(c.g) || b.Int() != int64(c.b) || a.Int() != int64(c.a) {
			t.Errorf("rl.%s = {%d,%d,%d,%d}, want {%d,%d,%d,%d}",
				c.name, r.Int(), g.Int(), b.Int(), a.Int(), c.r, c.g, c.b, c.a)
		}
	}
}
