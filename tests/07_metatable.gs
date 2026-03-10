// OOP with metatables
Animal := {}

Animal.new = func(name, sound) {
    self := {name: name, sound: sound}
    setmetatable(self, {__index: Animal})
    return self
}

Animal.speak = func(self) {
    return self.name .. " says " .. self.sound
}

dog := Animal.new("Rex", "woof")
cat := Animal.new("Whiskers", "meow")

print(dog.speak(dog))
print(cat.speak(cat))

// Arithmetic metatable
Vec2 := {}
Vec2.__add = func(a, b) {
    return Vec2.new(a.x + b.x, a.y + b.y)
}
Vec2.new = func(x, y) {
    v := {x: x, y: y}
    setmetatable(v, Vec2)
    return v
}

v1 := Vec2.new(1, 2)
v2 := Vec2.new(3, 4)
v3 := v1 + v2
print(v3.x)
print(v3.y)
