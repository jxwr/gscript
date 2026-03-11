# color

The `color` library provides an RGBA color type for GScript. Colors are tables with fields `r`, `g`, `b`, `a` (all in the 0.0 to 1.0 range) and metatables for operator overloading.

## Constructors

### color.new(r, g, b [, a]) -> color

Creates a color with components in the [0, 1] range. Alpha defaults to 1.0.

```
c := color.new(1, 0.5, 0)       -- orange, fully opaque
c := color.new(1, 0, 0, 0.5)    -- red, 50% transparent
```

### color.rgb(r, g, b) -> color

Creates a color from [0, 255] range values. Alpha is set to 1.0.

```
c := color.rgb(255, 128, 0)  -- r=1.0, g~0.502, b=0.0
```

### color.rgba(r, g, b, a) -> color

Creates a color from [0, 255] range values including alpha.

```
c := color.rgba(255, 0, 0, 128)  -- red, ~50% transparent
```

### color.fromHex(hexStr) -> color [, error]

Parses a hex color string. Supports `#RGB`, `#RRGGBB`, and `#RRGGBBAA` formats. Returns `nil, error` on invalid input.

```
c := color.fromHex("#FF0000")    -- pure red
c := color.fromHex("#F00")       -- same as above (shorthand)
c := color.fromHex("#FF000080")  -- red, ~50% alpha
```

### color.fromHSV(h, s, v) -> color

Creates a color from HSV values. `h` is in [0, 360], `s` and `v` are in [0, 1].

```
c := color.fromHSV(0, 1, 1)     -- pure red
c := color.fromHSV(120, 1, 1)   -- pure green
```

### color.fromHSL(h, s, l) -> color

Creates a color from HSL values. `h` is in [0, 360], `s` and `l` are in [0, 1].

```
c := color.fromHSL(0, 1, 0.5)   -- pure red
```

## Conversion Functions

### color.toHex(c) -> string

Returns the hex representation as `#RRGGBB`.

```
color.toHex(color.RED)  -- "#FF0000"
```

### color.toHSV(c) -> h, s, v

Converts to HSV. Returns three values.

### color.toHSL(c) -> h, s, l

Converts to HSL. Returns three values.

## Manipulation Functions

### color.lerp(c1, c2, t) -> color

Linear interpolation between two colors in RGB space. `t` ranges from 0 (c1) to 1 (c2).

### color.mix(c1, c2, t) -> color

Alias for `color.lerp`.

### color.darken(c, amount) -> color

Reduces brightness by `amount` (0-1). Multiplies RGB by `(1 - amount)`.

### color.lighten(c, amount) -> color

Increases brightness by `amount` (0-1). Formula: `component + (1 - component) * amount`.

### color.alpha(c, a) -> color

Returns the same color with a new alpha value.

### color.withAlpha(c, a) -> color

Alias for `color.alpha`.

### color.invert(c) -> color

Inverts the color: `(1-r, 1-g, 1-b, a)`. Alpha is preserved.

### color.grayscale(c) -> color

Converts to grayscale using standard luminance weights: `0.2126*R + 0.7152*G + 0.0722*B`.

### color.isColor(v) -> bool

Returns `true` if `v` is a color table (has `_type == "color"`).

## Operators

```
c1 := color.new(0.5, 0.3, 0.2, 1)
c2 := color.new(0.3, 0.9, 0.5, 1)

c3 := c1 + c2       -- component-wise add (clamped to 1)
c4 := c1 * 0.5      -- scale RGB by float (alpha preserved)
c1 == c2             -- compare r, g, b, a
```

## Named Constants

| Constant            | Value (r, g, b, a)   |
|---------------------|----------------------|
| `color.RED`         | (1, 0, 0, 1)        |
| `color.GREEN`       | (0, 1, 0, 1)        |
| `color.BLUE`        | (0, 0, 1, 1)        |
| `color.WHITE`       | (1, 1, 1, 1)        |
| `color.BLACK`       | (0, 0, 0, 1)        |
| `color.YELLOW`      | (1, 1, 0, 1)        |
| `color.CYAN`        | (0, 1, 1, 1)        |
| `color.MAGENTA`     | (1, 0, 1, 1)        |
| `color.ORANGE`      | (1, 0.5, 0, 1)      |
| `color.PURPLE`      | (0.5, 0, 0.5, 1)    |
| `color.TRANSPARENT` | (0, 0, 0, 0)        |
