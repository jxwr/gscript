# rl

The `rl` library provides bindings to [raylib](https://www.raylib.com/) via [raylib-go](https://github.com/gen2brain/raylib-go), enabling game development in GScript. It covers window management, 2D rendering, input handling, audio, collision detection, and math utilities.

## Colors

Colors are represented as tables with `r`, `g`, `b`, `a` fields (0-255 integers):

```
red := {r: 230, g: 41, b: 55, a: 255}
```

The library provides predefined color constants (see Color Constants below).

## Window Management

#### rl.initWindow(width, height, title)

Creates a window with the given dimensions and title.

```
rl.initWindow(800, 600, "My Game")
```

#### rl.closeWindow()

Closes the window and releases resources.

#### rl.windowShouldClose() -> bool

Returns `true` when the user requests to close the window (close button or ESC).

#### rl.setTargetFPS(fps)

Sets the target frames per second.

```
rl.setTargetFPS(60)
```

#### rl.getFPS() -> int

Returns the current FPS.

#### rl.getFrameTime() -> float

Returns the time in seconds for the last frame (delta time).

#### rl.getTime() -> float

Returns the elapsed time in seconds since `initWindow`.

#### rl.setWindowTitle(title)

Changes the window title.

#### rl.setWindowSize(w, h)

Resizes the window.

#### rl.getScreenWidth() -> int

Returns the screen/window width.

#### rl.getScreenHeight() -> int

Returns the screen/window height.

#### rl.isWindowReady() -> bool

Returns `true` if the window has been initialized.

#### rl.isWindowMinimized() -> bool

Returns `true` if the window is minimized.

#### rl.isWindowFocused() -> bool

Returns `true` if the window has input focus.

#### rl.setWindowPosition(x, y)

Sets the window position on the screen.

#### rl.toggleFullscreen()

Toggles fullscreen mode.

## Drawing

#### rl.beginDrawing()

Begins a drawing frame. Must be called before any draw commands.

#### rl.endDrawing()

Ends a drawing frame. Swaps buffers and polls events.

#### rl.clearBackground(color)

Clears the screen with the given color.

```
rl.clearBackground(rl.RAYWHITE)
```

#### rl.beginMode2D(camera)

Begins 2D camera mode. Camera is a table:

```
camera := {offsetX: 0, offsetY: 0, targetX: 0, targetY: 0, rotation: 0, zoom: 1.0}
rl.beginMode2D(camera)
```

#### rl.endMode2D()

Ends 2D camera mode.

#### rl.beginScissorMode(x, y, w, h)

Begins scissor mode (clipping region).

#### rl.endScissorMode()

Ends scissor mode.

## Shapes

#### rl.drawPixel(x, y, color)

Draws a single pixel.

#### rl.drawLine(x1, y1, x2, y2, color)

Draws a line between two points.

#### rl.drawLineEx(x1, y1, x2, y2, thick, color)

Draws a line with configurable thickness (float).

#### rl.drawCircle(cx, cy, radius, color)

Draws a filled circle.

#### rl.drawCircleLines(cx, cy, radius, color)

Draws a circle outline.

#### rl.drawCircleGradient(cx, cy, radius, color1, color2)

Draws a gradient-filled circle (inner to outer color).

#### rl.drawEllipse(cx, cy, rx, ry, color)

Draws a filled ellipse.

#### rl.drawRectangle(x, y, w, h, color)

Draws a filled rectangle.

```
rl.drawRectangle(100, 100, 200, 150, rl.RED)
```

#### rl.drawRectangleLines(x, y, w, h, color)

Draws a rectangle outline.

#### rl.drawRectangleLinesEx(x, y, w, h, lineThick, color)

Draws a rectangle outline with configurable thickness.

#### rl.drawRectangleRounded(x, y, w, h, roundness, segments, color)

Draws a rounded rectangle. `roundness` is 0.0-1.0.

#### rl.drawRectangleGradientH(x, y, w, h, color1, color2)

Draws a horizontal gradient rectangle.

#### rl.drawRectangleGradientV(x, y, w, h, color1, color2)

Draws a vertical gradient rectangle.

#### rl.drawTriangle(v1x, v1y, v2x, v2y, v3x, v3y, color)

Draws a filled triangle.

#### rl.drawTriangleLines(v1x, v1y, v2x, v2y, v3x, v3y, color)

Draws a triangle outline.

#### rl.drawPoly(cx, cy, sides, radius, rotation, color)

Draws a regular polygon.

#### rl.drawPolyLines(cx, cy, sides, radius, rotation, color)

Draws a regular polygon outline.

#### rl.drawRing(cx, cy, innerR, outerR, startAngle, endAngle, segments, color)

Draws a ring (annulus sector).

## Text

#### rl.drawText(text, x, y, fontSize, color)

Draws text using the default font.

```
rl.drawText("Hello World", 10, 10, 20, rl.BLACK)
```

#### rl.drawTextEx(font, text, x, y, fontSize, spacing, color)

Draws text using a loaded font with custom spacing.

#### rl.measureText(text, fontSize) -> int

Returns the width of text in pixels using the default font.

#### rl.measureTextEx(font, text, fontSize, spacing) -> width, height

Returns the dimensions of text using a loaded font (two return values).

#### rl.loadFont(path) -> font

Loads a font from a file. Returns a font table.

#### rl.unloadFont(font)

Unloads a previously loaded font.

#### rl.getFontDefault() -> font

Returns the default built-in font.

## Textures

Textures are represented as tables with `width`, `height`, and an internal `__texture_id` field.

#### rl.loadTexture(path) -> texture

Loads a texture from an image file (PNG, BMP, etc.).

```
tex := rl.loadTexture("assets/player.png")
```

#### rl.unloadTexture(texture)

Unloads a texture from GPU memory.

#### rl.drawTexture(texture, x, y, color)

Draws a texture at the given position.

```
rl.drawTexture(tex, 100, 100, rl.WHITE)
```

#### rl.drawTextureEx(texture, x, y, rotation, scale, color)

Draws a texture with rotation and scaling.

#### rl.drawTextureRec(texture, srcX, srcY, srcW, srcH, dstX, dstY, color)

Draws a sub-region of a texture (useful for sprite sheets).

#### rl.drawTexturePro(texture, srcX, srcY, srcW, srcH, dstX, dstY, dstW, dstH, originX, originY, rotation, color)

Draws a texture with full control over source rectangle, destination rectangle, origin, and rotation.

#### rl.genTextureMipmaps(texture) -> texture

Generates mipmaps for a texture. Returns the updated texture.

## Keyboard Input

#### rl.isKeyDown(key) -> bool

Returns `true` while the key is held down.

#### rl.isKeyUp(key) -> bool

Returns `true` while the key is not pressed.

#### rl.isKeyPressed(key) -> bool

Returns `true` only on the frame the key was first pressed.

#### rl.isKeyReleased(key) -> bool

Returns `true` only on the frame the key was released.

#### rl.getKeyPressed() -> int

Returns the key code of the last key pressed.

#### rl.setExitKey(key)

Sets which key will close the window (default: ESC). Use `rl.KEY_NULL` (0) to disable.

### Key Constants

```
rl.KEY_NULL, rl.KEY_SPACE, rl.KEY_ESCAPE, rl.KEY_ENTER, rl.KEY_TAB
rl.KEY_BACKSPACE, rl.KEY_INSERT, rl.KEY_DELETE
rl.KEY_RIGHT, rl.KEY_LEFT, rl.KEY_DOWN, rl.KEY_UP
rl.KEY_PAGE_UP, rl.KEY_PAGE_DOWN, rl.KEY_HOME, rl.KEY_END
rl.KEY_F1 .. rl.KEY_F12
rl.KEY_A .. rl.KEY_Z
rl.KEY_ZERO .. rl.KEY_NINE
rl.KEY_APOSTROPHE, rl.KEY_COMMA, rl.KEY_MINUS, rl.KEY_PERIOD
rl.KEY_SLASH, rl.KEY_SEMICOLON, rl.KEY_EQUAL
rl.KEY_LEFT_SHIFT, rl.KEY_LEFT_CONTROL, rl.KEY_LEFT_ALT
rl.KEY_RIGHT_SHIFT, rl.KEY_RIGHT_CONTROL, rl.KEY_RIGHT_ALT
```

## Mouse Input

#### rl.isMouseButtonDown(button) -> bool

Returns `true` while the mouse button is held.

#### rl.isMouseButtonPressed(button) -> bool

Returns `true` only on the frame the button was pressed.

#### rl.isMouseButtonReleased(button) -> bool

Returns `true` only on the frame the button was released.

#### rl.getMouseX() -> int

Returns the mouse X position.

#### rl.getMouseY() -> int

Returns the mouse Y position.

#### rl.getMousePosition() -> x, y

Returns the mouse position as two values.

```
x, y := rl.getMousePosition()
```

#### rl.getMouseDelta() -> dx, dy

Returns the mouse movement since the last frame.

#### rl.getMouseWheelMove() -> float

Returns the mouse wheel movement.

#### rl.setMousePosition(x, y)

Sets the mouse cursor position.

#### rl.setMouseCursor(cursor)

Sets the mouse cursor shape.

#### rl.showCursor() / rl.hideCursor()

Shows or hides the mouse cursor.

#### rl.isCursorHidden() -> bool

Returns `true` if the cursor is hidden.

### Mouse Button Constants

```
rl.MOUSE_BUTTON_LEFT   = 0
rl.MOUSE_BUTTON_RIGHT  = 1
rl.MOUSE_BUTTON_MIDDLE = 2
```

## Audio

#### rl.initAudioDevice()

Initializes the audio device. Call once before using audio functions.

#### rl.closeAudioDevice()

Closes the audio device.

#### rl.isAudioDeviceReady() -> bool

Returns `true` if the audio device is ready.

#### rl.setMasterVolume(vol)

Sets the master volume (0.0 to 1.0).

### Sound (short audio clips)

#### rl.loadSound(path) -> sound

Loads a sound from a file (WAV, OGG, etc.).

#### rl.unloadSound(sound)

Unloads a sound.

#### rl.playSound(sound) / rl.stopSound(sound)

Plays or stops a sound.

#### rl.pauseSound(sound) / rl.resumeSound(sound)

Pauses or resumes a sound.

#### rl.isSoundPlaying(sound) -> bool

Returns `true` if the sound is currently playing.

#### rl.setSoundVolume(sound, vol)

Sets the volume for a sound (0.0 to 1.0).

#### rl.setSoundPitch(sound, pitch)

Sets the pitch for a sound (1.0 = normal).

### Music (streaming audio)

#### rl.loadMusicStream(path) -> music

Loads a music stream from a file.

#### rl.unloadMusicStream(music)

Unloads a music stream.

#### rl.playMusicStream(music) / rl.stopMusicStream(music)

Plays or stops music.

#### rl.pauseMusicStream(music) / rl.resumeMusicStream(music)

Pauses or resumes music.

#### rl.updateMusicStream(music)

Updates the music stream buffer. **Must be called every frame** while music is playing.

#### rl.isMusicStreamPlaying(music) -> bool

Returns `true` if the music stream is playing.

#### rl.setMusicVolume(music, vol)

Sets the volume for music (0.0 to 1.0).

## Collision Detection

#### rl.checkCollisionRecs(x1, y1, w1, h1, x2, y2, w2, h2) -> bool

Checks collision between two rectangles.

#### rl.checkCollisionCircles(cx1, cy1, r1, cx2, cy2, r2) -> bool

Checks collision between two circles.

#### rl.checkCollisionCircleRec(cx, cy, r, x, y, w, h) -> bool

Checks collision between a circle and a rectangle.

#### rl.checkCollisionPointRec(px, py, x, y, w, h) -> bool

Checks if a point is inside a rectangle.

#### rl.checkCollisionPointCircle(px, py, cx, cy, r) -> bool

Checks if a point is inside a circle.

#### rl.getCollisionRec(x1, y1, w1, h1, x2, y2, w2, h2) -> x, y, w, h

Returns the overlap rectangle between two rectangles (four return values).

## Math Utilities

#### rl.vector2Length(x, y) -> float

Returns the length of a 2D vector.

#### rl.vector2Normalize(x, y) -> nx, ny

Returns the normalized vector (two return values).

#### rl.vector2Distance(x1, y1, x2, y2) -> float

Returns the distance between two points.

#### rl.vector2Lerp(x1, y1, x2, y2, t) -> rx, ry

Linearly interpolates between two vectors.

#### rl.fade(color, alpha) -> color

Returns a color with modified alpha (0.0 to 1.0).

#### rl.colorAlpha(color, alpha) -> color

Returns a color with the given alpha value.

#### rl.colorBrightness(color, factor) -> color

Adjusts the brightness of a color.

#### rl.colorContrast(color, contrast) -> color

Adjusts the contrast of a color.

#### rl.colorToHSV(color) -> h, s, v

Converts a color to HSV (three return values).

#### rl.colorFromHSV(h, s, v) -> color

Creates a color from HSV values.

#### rl.getRandomValue(min, max) -> int

Returns a random integer between min and max (inclusive).

## Color Constants

```
rl.LIGHTGRAY   -- {r: 200, g: 200, b: 200, a: 255}
rl.GRAY        -- {r: 130, g: 130, b: 130, a: 255}
rl.DARKGRAY    -- {r: 80,  g: 80,  b: 80,  a: 255}
rl.YELLOW      -- {r: 253, g: 249, b: 0,   a: 255}
rl.GOLD        -- {r: 255, g: 203, b: 0,   a: 255}
rl.ORANGE      -- {r: 255, g: 161, b: 0,   a: 255}
rl.PINK        -- {r: 255, g: 109, b: 194, a: 255}
rl.RED         -- {r: 230, g: 41,  b: 55,  a: 255}
rl.MAROON      -- {r: 190, g: 33,  b: 55,  a: 255}
rl.GREEN       -- {r: 0,   g: 228, b: 48,  a: 255}
rl.LIME        -- {r: 0,   g: 158, b: 47,  a: 255}
rl.DARKGREEN   -- {r: 0,   g: 117, b: 44,  a: 255}
rl.SKYBLUE     -- {r: 102, g: 191, b: 255, a: 255}
rl.BLUE        -- {r: 0,   g: 121, b: 241, a: 255}
rl.DARKBLUE    -- {r: 0,   g: 82,  b: 172, a: 255}
rl.PURPLE      -- {r: 200, g: 122, b: 255, a: 255}
rl.VIOLET      -- {r: 135, g: 60,  b: 190, a: 255}
rl.DARKPURPLE  -- {r: 112, g: 31,  b: 126, a: 255}
rl.BEIGE       -- {r: 211, g: 176, b: 131, a: 255}
rl.BROWN       -- {r: 127, g: 106, b: 79,  a: 255}
rl.DARKBROWN   -- {r: 76,  g: 63,  b: 47,  a: 255}
rl.WHITE       -- {r: 255, g: 255, b: 255, a: 255}
rl.BLACK       -- {r: 0,   g: 0,   b: 0,   a: 255}
rl.BLANK       -- {r: 0,   g: 0,   b: 0,   a: 0}
rl.MAGENTA     -- {r: 255, g: 0,   b: 255, a: 255}
rl.RAYWHITE    -- {r: 245, g: 245, b: 245, a: 255}
```

## Example: Minimal Game Loop

```
-- Initialize
rl.initWindow(800, 600, "GScript Game")
rl.setTargetFPS(60)

x := 400
y := 300
speed := 200

-- Main loop
while not rl.windowShouldClose() do
    dt := rl.getFrameTime()

    -- Input
    if rl.isKeyDown(rl.KEY_RIGHT) then x = x + speed * dt end
    if rl.isKeyDown(rl.KEY_LEFT)  then x = x - speed * dt end
    if rl.isKeyDown(rl.KEY_DOWN)  then y = y + speed * dt end
    if rl.isKeyDown(rl.KEY_UP)    then y = y - speed * dt end

    -- Draw
    rl.beginDrawing()
    rl.clearBackground(rl.RAYWHITE)
    rl.drawRectangle(x - 25, y - 25, 50, 50, rl.RED)
    rl.drawText("Move with arrow keys", 10, 10, 20, rl.DARKGRAY)
    rl.drawText("FPS: " .. rl.getFPS(), 10, 40, 20, rl.LIME)
    rl.endDrawing()
end

rl.closeWindow()
```
