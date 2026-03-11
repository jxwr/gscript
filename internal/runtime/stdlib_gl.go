package runtime

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"unsafe"

	"github.com/go-gl/gl/v4.1-core/gl"
	"github.com/go-gl/glfw/v3.3/glfw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Global GL state (only one window supported)
var glState struct {
	once   sync.Once
	window *glfw.Window

	rectProgram uint32
	textProgram uint32

	rectVAO uint32
	rectVBO uint32

	texVAO uint32
	texVBO uint32

	width  int // window width in screen coordinates (for rendering math)
	height int // window height in screen coordinates (for rendering math)
	fbW    int // framebuffer width (for gl.Viewport)
	fbH    int // framebuffer height (for gl.Viewport)

	// Key state
	keyDown        [glfw.KeyLast + 1]bool
	keyJustPressed [glfw.KeyLast + 1]bool
	keyPrevDown    [glfw.KeyLast + 1]bool

	// Font texture atlas
	fontTex    uint32
	fontCharW  int
	fontCharH  int
	fontAtlasW int
	fontAtlasH int
	fontCols   int
}

// ---------------------------------------------------------------------------
// Shader sources
// ---------------------------------------------------------------------------

var rectVertSrc = `#version 410 core
layout(location = 0) in vec2 aPos;
uniform vec2 uResolution;
uniform vec4 uRect;
uniform vec4 uColor;
out vec4 vColor;
void main() {
    vec2 pos = uRect.xy + aPos * uRect.zw;
    vec2 ndc = (pos / uResolution) * 2.0 - 1.0;
    ndc.y = -ndc.y;
    gl_Position = vec4(ndc, 0.0, 1.0);
    vColor = uColor;
}
` + "\x00"

var rectFragSrc = `#version 410 core
in vec4 vColor;
out vec4 fragColor;
void main() {
    fragColor = vColor;
}
` + "\x00"

var texVertSrc = `#version 410 core
layout(location = 0) in vec2 aPos;
uniform vec2 uResolution;
uniform vec2 uPos;
uniform vec2 uSize;
uniform vec4 uColor;
uniform vec2 uUVOffset;
uniform vec2 uUVSize;
out vec2 vUV;
out vec4 vColor;
void main() {
    vec2 pos = uPos + aPos * uSize;
    vec2 ndc = (pos / uResolution) * 2.0 - 1.0;
    ndc.y = -ndc.y;
    gl_Position = vec4(ndc, 0.0, 1.0);
    vUV = uUVOffset + aPos * uUVSize;
    vColor = uColor;
}
` + "\x00"

var texFragSrc = `#version 410 core
in vec2 vUV;
in vec4 vColor;
uniform sampler2D uTex;
out vec4 fragColor;
void main() {
    float a = texture(uTex, vUV).r;
    fragColor = vec4(vColor.rgb, vColor.a * a);
}
` + "\x00"

// ---------------------------------------------------------------------------
// Shader compilation helpers
// ---------------------------------------------------------------------------

func compileShader(source string, shaderType uint32) (uint32, error) {
	shader := gl.CreateShader(shaderType)
	csrc, free := gl.Strs(source)
	gl.ShaderSource(shader, 1, csrc, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &logLength)
		log := strings.Repeat("\x00", int(logLength+1))
		gl.GetShaderInfoLog(shader, logLength, nil, gl.Str(log))
		return 0, fmt.Errorf("shader compile error: %s", log)
	}
	return shader, nil
}

func linkProgram(vertSrc, fragSrc string) (uint32, error) {
	vert, err := compileShader(vertSrc, gl.VERTEX_SHADER)
	if err != nil {
		return 0, fmt.Errorf("vertex: %w", err)
	}
	frag, err := compileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		return 0, fmt.Errorf("fragment: %w", err)
	}

	prog := gl.CreateProgram()
	gl.AttachShader(prog, vert)
	gl.AttachShader(prog, frag)
	gl.LinkProgram(prog)

	var status int32
	gl.GetProgramiv(prog, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetProgramiv(prog, gl.INFO_LOG_LENGTH, &logLength)
		log := strings.Repeat("\x00", int(logLength+1))
		gl.GetProgramInfoLog(prog, logLength, nil, gl.Str(log))
		return 0, fmt.Errorf("program link error: %s", log)
	}

	gl.DeleteShader(vert)
	gl.DeleteShader(frag)
	return prog, nil
}

// ---------------------------------------------------------------------------
// VAO/VBO setup
// ---------------------------------------------------------------------------

func initRectVAO() {
	vertices := []float32{
		0, 0, // top-left
		1, 0, // top-right
		1, 1, // bottom-right
		0, 1, // bottom-left
	}

	gl.GenVertexArrays(1, &glState.rectVAO)
	gl.GenBuffers(1, &glState.rectVBO)

	gl.BindVertexArray(glState.rectVAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, glState.rectVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(vertices)*4, unsafe.Pointer(&vertices[0]), gl.STATIC_DRAW)

	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, 2*4, 0)

	gl.BindVertexArray(0)
}

func initTexVAO() {
	// Unit quad: just position (UVs computed in shader from aPos)
	vertices := []float32{
		0, 0,
		1, 0,
		1, 1,
		0, 1,
	}

	gl.GenVertexArrays(1, &glState.texVAO)
	gl.GenBuffers(1, &glState.texVBO)

	gl.BindVertexArray(glState.texVAO)
	gl.BindBuffer(gl.ARRAY_BUFFER, glState.texVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(vertices)*4, unsafe.Pointer(&vertices[0]), gl.STATIC_DRAW)

	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, 2*4, 0)

	gl.BindVertexArray(0)
}

// ---------------------------------------------------------------------------
// Font texture atlas
// ---------------------------------------------------------------------------

func initFontTexture() {
	face := basicfont.Face7x13
	charW := 7
	charH := 13
	cols := 16
	rows := 6
	atlasW := cols * charW // 112
	atlasH := rows * charH // 78

	img := image.NewGray(image.Rect(0, 0, atlasW, atlasH))
	// Fill with black background
	for y := 0; y < atlasH; y++ {
		for x := 0; x < atlasW; x++ {
			img.SetGray(x, y, color.Gray{0})
		}
	}

	for i := 0; i < 96; i++ {
		ch := rune(32 + i)
		col := i % cols
		row := i / cols
		x := col * charW
		y := row * charH

		d := &font.Drawer{
			Dst:  img,
			Src:  image.White,
			Face: face,
			Dot:  fixed.P(x, y+charH-2), // baseline position
		}
		d.DrawString(string(ch))
	}

	gl.GenTextures(1, &glState.fontTex)
	gl.BindTexture(gl.TEXTURE_2D, glState.fontTex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RED,
		int32(atlasW), int32(atlasH), 0,
		gl.RED, gl.UNSIGNED_BYTE, unsafe.Pointer(&img.Pix[0]))

	glState.fontCharW = charW
	glState.fontCharH = charH
	glState.fontAtlasW = atlasW
	glState.fontAtlasH = atlasH
	glState.fontCols = cols
}

// ---------------------------------------------------------------------------
// Drawing functions
// ---------------------------------------------------------------------------

func glDrawRect(x, y, w, h, r, g, b, a float32) {
	gl.UseProgram(glState.rectProgram)

	resLoc := gl.GetUniformLocation(glState.rectProgram, gl.Str("uResolution\x00"))
	rectLoc := gl.GetUniformLocation(glState.rectProgram, gl.Str("uRect\x00"))
	colorLoc := gl.GetUniformLocation(glState.rectProgram, gl.Str("uColor\x00"))

	res := [2]float32{float32(glState.width), float32(glState.height)}
	gl.Uniform2fv(resLoc, 1, &res[0])

	rect := [4]float32{x, y, w, h}
	gl.Uniform4fv(rectLoc, 1, &rect[0])

	col := [4]float32{r, g, b, a}
	gl.Uniform4fv(colorLoc, 1, &col[0])

	gl.BindVertexArray(glState.rectVAO)
	gl.DrawArrays(gl.TRIANGLE_FAN, 0, 4)
	gl.BindVertexArray(0)
}

func glDrawRectOutline(x, y, w, h, r, g, b, lineW float32) {
	// Top
	glDrawRect(x, y, w, lineW, r, g, b, 1)
	// Bottom
	glDrawRect(x, y+h-lineW, w, lineW, r, g, b, 1)
	// Left
	glDrawRect(x, y, lineW, h, r, g, b, 1)
	// Right
	glDrawRect(x+w-lineW, y, lineW, h, r, g, b, 1)
}

func glDrawText(text string, x, y, scale, r, g, b float32) {
	gl.UseProgram(glState.textProgram)

	resLoc := gl.GetUniformLocation(glState.textProgram, gl.Str("uResolution\x00"))
	posLoc := gl.GetUniformLocation(glState.textProgram, gl.Str("uPos\x00"))
	sizeLoc := gl.GetUniformLocation(glState.textProgram, gl.Str("uSize\x00"))
	colorLoc := gl.GetUniformLocation(glState.textProgram, gl.Str("uColor\x00"))
	uvOffLoc := gl.GetUniformLocation(glState.textProgram, gl.Str("uUVOffset\x00"))
	uvSzLoc := gl.GetUniformLocation(glState.textProgram, gl.Str("uUVSize\x00"))

	res := [2]float32{float32(glState.width), float32(glState.height)}
	gl.Uniform2fv(resLoc, 1, &res[0])

	col := [4]float32{r, g, b, 1.0}
	gl.Uniform4fv(colorLoc, 1, &col[0])

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, glState.fontTex)

	charW := float32(glState.fontCharW) * scale
	charH := float32(glState.fontCharH) * scale

	uvCellW := float32(glState.fontCharW) / float32(glState.fontAtlasW)
	uvCellH := float32(glState.fontCharH) / float32(glState.fontAtlasH)

	gl.BindVertexArray(glState.texVAO)

	cx := x
	for _, ch := range text {
		idx := int(ch) - 32
		if idx < 0 || idx >= 96 {
			cx += charW
			continue
		}

		col := idx % glState.fontCols
		row := idx / glState.fontCols

		uvX := float32(col) * uvCellW
		uvY := float32(row) * uvCellH

		pos := [2]float32{cx, y}
		gl.Uniform2fv(posLoc, 1, &pos[0])

		sz := [2]float32{charW, charH}
		gl.Uniform2fv(sizeLoc, 1, &sz[0])

		uvOff := [2]float32{uvX, uvY}
		gl.Uniform2fv(uvOffLoc, 1, &uvOff[0])

		uvSz := [2]float32{uvCellW, uvCellH}
		gl.Uniform2fv(uvSzLoc, 1, &uvSz[0])

		gl.DrawArrays(gl.TRIANGLE_FAN, 0, 4)

		cx += charW
	}

	gl.BindVertexArray(0)
}

// ---------------------------------------------------------------------------
// Key state update
// ---------------------------------------------------------------------------

func updateKeyState() {
	for i := 0; i <= int(glfw.KeyLast); i++ {
		glState.keyJustPressed[i] = glState.keyDown[i] && !glState.keyPrevDown[i]
		glState.keyPrevDown[i] = glState.keyDown[i]
	}
}

// ---------------------------------------------------------------------------
// glLib: exposed to GScript
// ---------------------------------------------------------------------------

func glLib(interp *Interpreter) *Table {
	t := NewTable()

	set := func(name string, fn func([]Value) ([]Value, error)) {
		t.RawSet(StringValue(name), FunctionValue(&GoFunction{
			Name: "gl." + name,
			Fn:   fn,
		}))
	}

	// Key constants
	t.RawSet(StringValue("KEY_LEFT"), IntValue(int64(glfw.KeyLeft)))
	t.RawSet(StringValue("KEY_RIGHT"), IntValue(int64(glfw.KeyRight)))
	t.RawSet(StringValue("KEY_UP"), IntValue(int64(glfw.KeyUp)))
	t.RawSet(StringValue("KEY_DOWN"), IntValue(int64(glfw.KeyDown)))
	t.RawSet(StringValue("KEY_SPACE"), IntValue(int64(glfw.KeySpace)))
	t.RawSet(StringValue("KEY_ESCAPE"), IntValue(int64(glfw.KeyEscape)))
	t.RawSet(StringValue("KEY_ENTER"), IntValue(int64(glfw.KeyEnter)))

	// Letter keys
	for c := 'A'; c <= 'Z'; c++ {
		keyName := fmt.Sprintf("KEY_%c", c)
		glfwKey := glfw.KeyA + glfw.Key(c-'A')
		t.RawSet(StringValue(keyName), IntValue(int64(glfwKey)))
	}

	// Number keys
	for c := '0'; c <= '9'; c++ {
		keyName := fmt.Sprintf("KEY_%c", c)
		glfwKey := glfw.Key0 + glfw.Key(c-'0')
		t.RawSet(StringValue(keyName), IntValue(int64(glfwKey)))
	}

	// F-keys
	for i := 1; i <= 12; i++ {
		keyName := fmt.Sprintf("KEY_F%d", i)
		glfwKey := glfw.KeyF1 + glfw.Key(i-1)
		t.RawSet(StringValue(keyName), IntValue(int64(glfwKey)))
	}

	// gl.newWindow(width, height, title) -> windowTable
	set("newWindow", func(args []Value) ([]Value, error) {
		if len(args) < 3 {
			return nil, fmt.Errorf("gl.newWindow requires (width, height, title)")
		}
		w := int(toInt(args[0]))
		h := int(toInt(args[1]))
		title := args[2].Str()

		var initErr error
		glState.once.Do(func() {
			if err := glfw.Init(); err != nil {
				initErr = fmt.Errorf("glfw.Init: %w", err)
				return
			}
		})
		if initErr != nil {
			return nil, initErr
		}

		glfw.WindowHint(glfw.ContextVersionMajor, 4)
		glfw.WindowHint(glfw.ContextVersionMinor, 1)
		glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
		glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)
		glfw.WindowHint(glfw.Resizable, glfw.False)

		window, err := glfw.CreateWindow(w, h, title, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("glfw.CreateWindow: %w", err)
		}
		window.MakeContextCurrent()
		glfw.SwapInterval(1)

		if err := gl.Init(); err != nil {
			return nil, fmt.Errorf("gl.Init: %w", err)
		}

		glState.window = window

		// Store window size (screen coords) for rendering math
		glState.width = w
		glState.height = h

		// Use framebuffer size for gl.Viewport (handles Retina/HiDPI)
		fbW, fbH := window.GetFramebufferSize()
		glState.fbW = fbW
		glState.fbH = fbH
		gl.Viewport(0, 0, int32(fbW), int32(fbH))

		// Enable blending
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)

		// Compile shaders
		glState.rectProgram, err = linkProgram(rectVertSrc, rectFragSrc)
		if err != nil {
			return nil, fmt.Errorf("rect shader: %w", err)
		}
		glState.textProgram, err = linkProgram(texVertSrc, texFragSrc)
		if err != nil {
			return nil, fmt.Errorf("text shader: %w", err)
		}

		// Init VAOs
		initRectVAO()
		initTexVAO()

		// Init font
		initFontTexture()

		// Key callback
		window.SetKeyCallback(func(w *glfw.Window, key glfw.Key, scancode int, action glfw.Action, mods glfw.ModifierKey) {
			if key >= 0 && key <= glfw.KeyLast {
				if action == glfw.Press {
					glState.keyDown[key] = true
				} else if action == glfw.Release {
					glState.keyDown[key] = false
				}
			}
		})

		// Framebuffer size callback
		window.SetFramebufferSizeCallback(func(w *glfw.Window, width, height int) {
			glState.fbW = width
			glState.fbH = height
			gl.Viewport(0, 0, int32(width), int32(height))
		})

		// Window size callback (screen coords)
		window.SetSizeCallback(func(w *glfw.Window, width, height int) {
			glState.width = width
			glState.height = height
		})

		// Build window table
		winTable := NewTable()

		winSet := func(name string, fn func([]Value) ([]Value, error)) {
			winTable.RawSet(StringValue(name), FunctionValue(&GoFunction{
				Name: "window." + name,
				Fn:   fn,
			}))
		}

		winSet("shouldClose", func(args []Value) ([]Value, error) {
			return []Value{BoolValue(window.ShouldClose())}, nil
		})

		winSet("pollEvents", func(args []Value) ([]Value, error) {
			updateKeyState()
			glfw.PollEvents()
			return nil, nil
		})

		winSet("swapBuffers", func(args []Value) ([]Value, error) {
			window.SwapBuffers()
			return nil, nil
		})

		winSet("close", func(args []Value) ([]Value, error) {
			window.Destroy()
			glfw.Terminate()
			return nil, nil
		})

		winSet("setTitle", func(args []Value) ([]Value, error) {
			if len(args) < 1 {
				return nil, fmt.Errorf("window.setTitle requires a title")
			}
			window.SetTitle(args[0].Str())
			return nil, nil
		})

		winSet("clear", func(args []Value) ([]Value, error) {
			r, g, b := float32(0), float32(0), float32(0)
			if len(args) >= 3 {
				r = float32(toFloat(args[0]))
				g = float32(toFloat(args[1]))
				b = float32(toFloat(args[2]))
			}
			gl.ClearColor(r, g, b, 1.0)
			gl.Clear(gl.COLOR_BUFFER_BIT)
			return nil, nil
		})

		return []Value{TableValue(winTable)}, nil
	})

	// gl.drawRect(x, y, w, h, r, g, b, a)
	set("drawRect", func(args []Value) ([]Value, error) {
		if len(args) < 8 {
			return nil, fmt.Errorf("gl.drawRect requires 8 args (x, y, w, h, r, g, b, a)")
		}
		glDrawRect(
			float32(toFloat(args[0])),
			float32(toFloat(args[1])),
			float32(toFloat(args[2])),
			float32(toFloat(args[3])),
			float32(toFloat(args[4])),
			float32(toFloat(args[5])),
			float32(toFloat(args[6])),
			float32(toFloat(args[7])),
		)
		return nil, nil
	})

	// gl.drawRectOutline(x, y, w, h, r, g, b, lineWidth)
	set("drawRectOutline", func(args []Value) ([]Value, error) {
		if len(args) < 8 {
			return nil, fmt.Errorf("gl.drawRectOutline requires 8 args (x, y, w, h, r, g, b, lineWidth)")
		}
		glDrawRectOutline(
			float32(toFloat(args[0])),
			float32(toFloat(args[1])),
			float32(toFloat(args[2])),
			float32(toFloat(args[3])),
			float32(toFloat(args[4])),
			float32(toFloat(args[5])),
			float32(toFloat(args[6])),
			float32(toFloat(args[7])),
		)
		return nil, nil
	})

	// gl.drawText(text, x, y, scale, r, g, b)
	set("drawText", func(args []Value) ([]Value, error) {
		if len(args) < 7 {
			return nil, fmt.Errorf("gl.drawText requires 7 args (text, x, y, scale, r, g, b)")
		}
		glDrawText(
			args[0].String(),
			float32(toFloat(args[1])),
			float32(toFloat(args[2])),
			float32(toFloat(args[3])),
			float32(toFloat(args[4])),
			float32(toFloat(args[5])),
			float32(toFloat(args[6])),
		)
		return nil, nil
	})

	// gl.getTime() -> float64
	set("getTime", func(args []Value) ([]Value, error) {
		return []Value{FloatValue(glfw.GetTime())}, nil
	})

	// gl.isKeyDown(key) -> bool
	set("isKeyDown", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		key := int(toInt(args[0]))
		if key >= 0 && key <= int(glfw.KeyLast) {
			return []Value{BoolValue(glState.keyDown[key])}, nil
		}
		return []Value{BoolValue(false)}, nil
	})

	// gl.isKeyJustPressed(key) -> bool
	set("isKeyJustPressed", func(args []Value) ([]Value, error) {
		if len(args) < 1 {
			return []Value{BoolValue(false)}, nil
		}
		key := int(toInt(args[0]))
		if key >= 0 && key <= int(glfw.KeyLast) {
			return []Value{BoolValue(glState.keyJustPressed[key])}, nil
		}
		return []Value{BoolValue(false)}, nil
	})

	// gl.windowWidth() -> int
	set("windowWidth", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(glState.width))}, nil
	})

	// gl.windowHeight() -> int
	set("windowHeight", func(args []Value) ([]Value, error) {
		return []Value{IntValue(int64(glState.height))}, nil
	})

	return t
}
