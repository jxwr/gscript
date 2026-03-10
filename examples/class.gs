// Class-based OOP with GScript metatables
func Class(parent) {
    cls := {}
    cls.__index = cls
    if parent != nil {
        setmetatable(cls, {__index: parent})
    }
    cls.new = func(...) {
        instance := {}
        setmetatable(instance, cls)
        if cls.init != nil {
            cls.init(instance, ...)
        }
        return instance
    }
    return cls
}

// Define Animal class
Animal := Class(nil)
Animal.init = func(self, name) {
    self.name = name
}
Animal.speak = func(self) {
    return self.name .. " makes a sound"
}

// Dog extends Animal
Dog := Class(Animal)
Dog.init = func(self, name) {
    Animal.init(self, name)
    self.tricks = {}
}
Dog.speak = func(self) {
    return self.name .. " says woof!"
}
Dog.learn = func(self, trick) {
    table.insert(self.tricks, trick)
}
Dog.showTricks = func(self) {
    print(self.name .. " knows:")
    for _, trick := range self.tricks {
        print("  -", trick)
    }
}

fido := Dog.new("Fido")
print(fido.speak(fido))
fido.learn(fido, "sit")
fido.learn(fido, "shake")
fido.learn(fido, "roll over")
fido.showTricks(fido)
