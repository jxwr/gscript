# vec

The `vec` library provides 2D and 3D vector types for GScript. Vectors are tables with metatables that enable operator overloading (`+`, `-`, `*`, `/`, unary `-`, `==`).

## Vec2

A vec2 is a table with fields `x`, `y`, and `_type = "vec2"`.

### Constructors

#### vec.vec2(x, y) -> vec2

Creates a new 2D vector.

```
v := vec.vec2(3, 4)
print(v.x, v.y)  -- 3  4
```

#### vec.zero2() -> vec2

Returns `vec2(0, 0)`.

#### vec.one2() -> vec2

Returns `vec2(1, 1)`.

#### vec.up() -> vec2

Returns `vec2(0, 1)`.

#### vec.right() -> vec2

Returns `vec2(1, 0)`.

### Operators

```
v1 := vec.vec2(1, 2)
v2 := vec.vec2(3, 4)

v3 := v1 + v2       -- vec2(4, 6)
v4 := v1 - v2       -- vec2(-2, -2)
v5 := v1 * 2        -- vec2(2, 4)
v6 := 2 * v1        -- vec2(2, 4)
v7 := v1 / 2        -- vec2(0.5, 1)
v8 := -v1           -- vec2(-1, -2)
v1 == vec.vec2(1,2) -- true
```

### Utility Functions

#### vec.dot2(v1, v2) -> float

Returns the dot product of two vec2 values.

#### vec.length2(v) -> float

Returns the magnitude (length) of a vec2.

#### vec.lengthSq2(v) -> float

Returns the squared magnitude (faster than `length2`).

#### vec.normalize2(v) -> vec2

Returns a unit vector in the same direction. Returns `vec2(0, 0)` for zero-length vectors.

#### vec.angle2(v) -> float

Returns the angle in radians (`atan2(y, x)`).

#### vec.rotate2(v, angle) -> vec2

Rotates the vector by `angle` radians.

#### vec.lerp2(v1, v2, t) -> vec2

Linear interpolation between `v1` and `v2` at parameter `t` (0 = v1, 1 = v2).

#### vec.dist2(v1, v2) -> float

Returns the Euclidean distance between two points.

#### vec.distSq2(v1, v2) -> float

Returns the squared distance (faster than `dist2`).

#### vec.reflect2(v, normal) -> vec2

Reflects vector `v` across `normal`. Formula: `v - 2 * dot(v, normal) * normal`.

#### vec.perp2(v) -> vec2

Returns the perpendicular vector `(-y, x)`.

#### vec.clamp2(v, min, max) -> vec2

Clamps each component of `v` between `min` and `max`. The `min` and `max` arguments can be floats (applied to both components) or vec2 tables.

#### vec.isVec2(v) -> bool

Returns `true` if `v` is a vec2 table (has `_type == "vec2"`).

## Vec3

A vec3 is a table with fields `x`, `y`, `z`, and `_type = "vec3"`.

### Constructors

#### vec.vec3(x, y, z) -> vec3

Creates a new 3D vector.

```
v := vec.vec3(1, 2, 3)
print(v.x, v.y, v.z)  -- 1  2  3
```

#### vec.zero3() -> vec3

Returns `vec3(0, 0, 0)`.

#### vec.one3() -> vec3

Returns `vec3(1, 1, 1)`.

### Operators

Vec3 supports the same operators as vec2: `+`, `-`, `*` (scalar), `/` (scalar), unary `-`, `==`.

### Utility Functions

#### vec.dot3(v1, v2) -> float

Returns the dot product of two vec3 values.

#### vec.cross3(v1, v2) -> vec3

Returns the cross product.

#### vec.length3(v) -> float

Returns the magnitude of a vec3.

#### vec.normalize3(v) -> vec3

Returns a unit vector in the same direction.

#### vec.lerp3(v1, v2, t) -> vec3

Linear interpolation between two vec3 values.

#### vec.dist3(v1, v2) -> float

Returns the distance between two 3D points.

#### vec.isVec3(v) -> bool

Returns `true` if `v` is a vec3 table.
