// oop.gs - Object-oriented programming patterns in GScript
// Demonstrates: class system, inheritance, method calls, private fields,
//               mixins/multiple inheritance, instanceof check

print("=== Object-Oriented Programming Patterns ===")
print()

// -------------------------------------------------------
// 1. Class system using metatables
// -------------------------------------------------------
print("--- Class System ---")

// Base class factory
func Class(parent) {
    cls := {}
    cls.__index = cls

    // If there's a parent class, set up inheritance
    if parent != nil {
        setmetatable(cls, {__index: parent})
    }

    // Constructor: creates a new instance
    cls.new = func(...) {
        instance := {}
        setmetatable(instance, cls)
        if cls.init != nil {
            cls.init(instance, ...)
        }
        return instance
    }

    // Check if this class is or inherits from another
    cls.isSubclassOf = func(other) {
        current := cls
        for current != nil {
            if current == other { return true }
            mt := getmetatable(current)
            if mt == nil { return nil }
            current = mt.__index
            if current == nil { return nil }
        }
        return false
    }

    return cls
}

// -------------------------------------------------------
// 2. Inheritance chain: Animal -> Dog -> GoldenRetriever
// -------------------------------------------------------
print("--- Inheritance Chain ---")

// Animal base class
Animal := Class(nil)

Animal.init = func(self, name, sound) {
    self.name = name
    self.sound = sound
}

Animal.speak = func(self) {
    return self.name .. " says " .. self.sound
}

Animal.toString = func(self) {
    return "Animal(" .. self.name .. ")"
}

Animal.getType = func(self) {
    return "Animal"
}

// Dog extends Animal
Dog := Class(Animal)

Dog.init = func(self, name) {
    Animal.init(self, name, "Woof!")
    self.tricks = {}
}

Dog.learn = func(self, trick) {
    table.insert(self.tricks, trick)
}

Dog.showTricks = func(self) {
    if #self.tricks == 0 {
        return self.name .. " knows no tricks"
    }
    return self.name .. " knows: " .. table.concat(self.tricks, ", ")
}

Dog.getType = func(self) {
    return "Dog"
}

// GoldenRetriever extends Dog
GoldenRetriever := Class(Dog)

GoldenRetriever.init = func(self, name) {
    Dog.init(self, name)
    self.isFriendly = true
}

GoldenRetriever.fetch = func(self, item) {
    return self.name .. " fetches the " .. item .. "!"
}

GoldenRetriever.getType = func(self) {
    return "GoldenRetriever"
}

// Create instances
cat := Animal.new("Whiskers", "Meow!")
dog := Dog.new("Rex")
golden := GoldenRetriever.new("Buddy")

print("  " .. cat.speak(cat))
print("  " .. dog.speak(dog))           // Inherited from Animal
print("  " .. golden.speak(golden))     // Inherited through Dog -> Animal

dog.learn(dog, "sit")
dog.learn(dog, "shake")
print("  " .. dog.showTricks(dog))

golden.learn(golden, "roll over")
golden.learn(golden, "play dead")
print("  " .. golden.showTricks(golden))  // Inherited from Dog
print("  " .. golden.fetch(golden, "ball"))
print("  Golden is friendly:", golden.isFriendly)
print()

// -------------------------------------------------------
// 3. Method calls pattern (passing self explicitly)
// -------------------------------------------------------
print("--- Method Calls ---")

// In GScript, we pass self explicitly: obj.method(obj, args)
// This is clear and straightforward
print("  " .. dog.getType(dog) .. ": " .. dog.speak(dog))
print("  " .. golden.getType(golden) .. ": " .. golden.speak(golden))

// You can also store a bound method
boundSpeak := func() { return golden.speak(golden) }
print("  Bound method call: " .. boundSpeak())
print()

// -------------------------------------------------------
// 4. Private fields via closures
// -------------------------------------------------------
print("--- Private Fields via Closures ---")

func createPerson(name, age) {
    // Private state - only accessible through closures
    _name := name
    _age := age
    _secret := "I like ice cream"

    person := {}

    // Public getters
    person.getName = func() { return _name }
    person.getAge = func() { return _age }

    // Public setter with validation
    person.setAge = func(newAge) {
        if newAge < 0 {
            error("Age cannot be negative")
        }
        _age = newAge
    }

    // Public method that uses private data
    person.greet = func() {
        return "Hi, I'm " .. _name .. " and I'm " .. tostring(_age) .. " years old"
    }

    // The secret is truly private - no way to access it from outside
    person.hasSecret = func() { return true }

    return person
}

alice := createPerson("Alice", 30)
print("  " .. alice.greet())
print("  Name: " .. alice.getName())
alice.setAge(31)
print("  After birthday: " .. alice.greet())

// Try to set negative age
ok, err := pcall(func() { alice.setAge(-5) })
print("  Set negative age - ok:", ok, "err:", err)

// Private fields are inaccessible
print("  Has secret:", alice.hasSecret())
print("  Direct access to _secret:", alice._secret)  // nil - can't access
print()

// -------------------------------------------------------
// 5. Mixins / Multiple inheritance pattern
// -------------------------------------------------------
print("--- Mixins ---")

// A mixin is a table of methods that can be mixed into a class
func Mixin(target, mixin) {
    for key, value := range mixin {
        if target[key] == nil {
            target[key] = value
        }
    }
    return target
}

// Serializable mixin
Serializable := {
    serialize: func(self) {
        parts := {}
        for k, v := range self {
            if type(v) != "function" {
                table.insert(parts, tostring(k) .. "=" .. tostring(v))
            }
        }
        return "{" .. table.concat(parts, ", ") .. "}"
    }
}

// Comparable mixin
Comparable := {
    isEqual: func(self, other) {
        return self.compareTo(self, other) == 0
    },
    isGreaterThan: func(self, other) {
        return self.compareTo(self, other) > 0
    },
    isLessThan: func(self, other) {
        return self.compareTo(self, other) < 0
    }
}

// Create a class that uses mixins
Score := Class(nil)
Mixin(Score, Serializable)
Mixin(Score, Comparable)

Score.init = func(self, player, points) {
    self.player = player
    self.points = points
}

Score.compareTo = func(self, other) {
    return self.points - other.points
}

s1 := Score.new("Alice", 100)
s2 := Score.new("Bob", 85)

print("  Score 1:", s1.serialize(s1))
print("  Score 2:", s2.serialize(s2))
print("  s1 > s2:", s1.isGreaterThan(s1, s2))
print("  s1 < s2:", s1.isLessThan(s1, s2))
print("  s1 == s2:", s1.isEqual(s1, s2))
print()

// -------------------------------------------------------
// 6. instanceof check
// -------------------------------------------------------
print("--- instanceof Check ---")

func instanceof(obj, cls) {
    mt := getmetatable(obj)
    for mt != nil {
        if mt == cls {
            return true
        }
        // Go up the chain
        parentMt := getmetatable(mt)
        if parentMt == nil { return false }
        mt = parentMt.__index
        if mt == nil { return false }
    }
    return false
}

print("  golden instanceof GoldenRetriever:", instanceof(golden, GoldenRetriever))
print("  golden instanceof Dog:", instanceof(golden, Dog))
print("  golden instanceof Animal:", instanceof(golden, Animal))
print("  dog instanceof Dog:", instanceof(dog, Dog))
print("  dog instanceof Animal:", instanceof(dog, Animal))
print("  dog instanceof GoldenRetriever:", instanceof(dog, GoldenRetriever))
print("  cat instanceof Animal:", instanceof(cat, Animal))
print("  cat instanceof Dog:", instanceof(cat, Dog))
print()

// -------------------------------------------------------
// 7. Practical OOP example: Shape hierarchy
// -------------------------------------------------------
print("--- Shape Hierarchy Example ---")

Shape := Class(nil)
Shape.init = func(self, kind) {
    self.kind = kind
}
Shape.area = func(self) { return 0 }
Shape.describe = func(self) {
    return self.kind .. " with area " .. string.format("%.2f", self.area(self))
}

Circle := Class(Shape)
Circle.init = func(self, radius) {
    Shape.init(self, "Circle")
    self.radius = radius
}
Circle.area = func(self) {
    return math.pi * self.radius * self.radius
}

Rectangle := Class(Shape)
Rectangle.init = func(self, width, height) {
    Shape.init(self, "Rectangle")
    self.width = width
    self.height = height
}
Rectangle.area = func(self) {
    return self.width * self.height
}

Triangle := Class(Shape)
Triangle.init = func(self, base, height) {
    Shape.init(self, "Triangle")
    self.base = base
    self.height = height
}
Triangle.area = func(self) {
    return 0.5 * self.base * self.height
}

shapes := {
    Circle.new(5),
    Rectangle.new(4, 6),
    Triangle.new(3, 8)
}

totalArea := 0
for _, shape := range shapes {
    print("  " .. shape.describe(shape))
    totalArea = totalArea + shape.area(shape)
}
print(string.format("  Total area: %.2f", totalArea))
print()

print("=== Done ===")
