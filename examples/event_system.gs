// event_system.gs - Event emitter pattern in GScript
// Demonstrates: EventEmitter class, on, emit, once, off, game-like event system

print("=== Event System ===")
print()

// -------------------------------------------------------
// 1. EventEmitter class
// -------------------------------------------------------
print("--- EventEmitter ---")

func newEventEmitter() {
    handlers := {}
    onceFlags := {}
    emitter := {}

    // Register a handler for an event
    emitter.on = func(event, handler) {
        if handlers[event] == nil {
            handlers[event] = {}
        }
        table.insert(handlers[event], handler)
        return emitter  // for chaining
    }

    // Register a handler that fires only once
    emitter.once = func(event, handler) {
        // Use a table to allow self-reference from within the wrapper
        ref := {}
        ref.fn = func(...) {
            handler(...)
            // Remove ourselves after firing
            emitter.off(event, ref.fn)
        }
        emitter.on(event, ref.fn)
        return emitter
    }

    // Emit an event with arguments
    emitter.emit = func(event, ...) {
        args := {...}
        eventHandlers := handlers[event]
        if eventHandlers == nil { return emitter }

        // Copy handler list in case of modifications during iteration
        toCall := {}
        for i := 1; i <= #eventHandlers; i++ {
            table.insert(toCall, eventHandlers[i])
        }

        for i := 1; i <= #toCall; i++ {
            toCall[i](unpack(args))
        }
        return emitter
    }

    // Remove a specific handler for an event
    emitter.off = func(event, handler) {
        if handlers[event] == nil { return emitter }
        newHandlers := {}
        for i := 1; i <= #handlers[event]; i++ {
            if handlers[event][i] != handler {
                table.insert(newHandlers, handlers[event][i])
            }
        }
        handlers[event] = newHandlers
        return emitter
    }

    // Remove all handlers for an event (or all events if no event specified)
    emitter.removeAll = func(event) {
        if event != nil {
            handlers[event] = {}
        } else {
            handlers = {}
        }
        return emitter
    }

    // Get number of listeners for an event
    emitter.listenerCount = func(event) {
        if handlers[event] == nil { return 0 }
        return #handlers[event]
    }

    return emitter
}

// -------------------------------------------------------
// 2. Basic usage
// -------------------------------------------------------
print("--- Basic Usage ---")

emitter := newEventEmitter()

// Register handlers
emitter.on("greet", func(name) {
    print("  Hello, " .. name .. "!")
})

emitter.on("greet", func(name) {
    print("  Welcome aboard, " .. name .. "!")
})

emitter.emit("greet", "Alice")
print()

// -------------------------------------------------------
// 3. Once - handler fires only once
// -------------------------------------------------------
print("--- Once ---")

emitter2 := newEventEmitter()

emitter2.on("tick", func(n) {
    print("  [always] tick #" .. tostring(n))
})

emitter2.once("tick", func(n) {
    print("  [once] tick #" .. tostring(n) .. " (first tick only!)")
})

emitter2.emit("tick", 1)
emitter2.emit("tick", 2)
emitter2.emit("tick", 3)
print()

// -------------------------------------------------------
// 4. Off - remove a handler
// -------------------------------------------------------
print("--- Off (remove handler) ---")

emitter3 := newEventEmitter()

handler := func(msg) {
    print("  Handler A: " .. msg)
}

emitter3.on("msg", handler)
emitter3.on("msg", func(msg) {
    print("  Handler B: " .. msg)
})

print("  Before removing Handler A:")
emitter3.emit("msg", "hello")

emitter3.off("msg", handler)
print("  After removing Handler A:")
emitter3.emit("msg", "hello")
print()

// -------------------------------------------------------
// 5. Game-like event system demo
// -------------------------------------------------------
print("--- Game Event System Demo ---")

// Create a game event bus
gameEvents := newEventEmitter()

// Game state
gameState := {
    score: 0,
    lives: 3,
    level: 1,
    combo: 0
}

// Score system listens for enemy-killed events
gameEvents.on("enemy-killed", func(enemy) {
    points := enemy.points
    gameState.combo = gameState.combo + 1
    bonus := gameState.combo * 10
    gameState.score = gameState.score + points + bonus
    print(string.format("    [Score] +%d points (+%d combo bonus) = %d total",
        points, bonus, gameState.score))
})

// Life system
gameEvents.on("player-hit", func(damage) {
    gameState.lives = gameState.lives - 1
    gameState.combo = 0
    print(string.format("    [Life] Player hit! Lives remaining: %d", gameState.lives))
    if gameState.lives <= 0 {
        gameEvents.emit("game-over")
    }
})

// Level system
gameEvents.on("level-complete", func() {
    gameState.level = gameState.level + 1
    gameState.combo = 0
    print(string.format("    [Level] Level up! Now on level %d", gameState.level))
    gameEvents.emit("level-start", gameState.level)
})

gameEvents.on("level-start", func(level) {
    print(string.format("    [Game] Level %d started!", level))
})

// Achievement system (one-time events)
gameEvents.once("enemy-killed", func(enemy) {
    print("    [Achievement] First Kill!")
})

// Game over handler
gameEvents.on("game-over", func() {
    print(string.format("    [Game] GAME OVER! Final score: %d, Level: %d", gameState.score, gameState.level))
})

// Simulate a game session
print("  === Game Session ===")
print()

print("  Killing enemies:")
gameEvents.emit("enemy-killed", {name: "Goblin", points: 50})
gameEvents.emit("enemy-killed", {name: "Orc", points: 100})
gameEvents.emit("enemy-killed", {name: "Dragon", points: 500})
print()

print("  Player gets hit:")
gameEvents.emit("player-hit", 10)
print()

print("  More enemies:")
gameEvents.emit("enemy-killed", {name: "Goblin", points: 50})
gameEvents.emit("enemy-killed", {name: "Goblin", points: 50})
print()

print("  Level complete:")
gameEvents.emit("level-complete")
print()

print("  Player gets hit (twice):")
gameEvents.emit("player-hit", 20)
gameEvents.emit("player-hit", 30)
print()

// -------------------------------------------------------
// 6. Event-driven counter with multiple listeners
// -------------------------------------------------------
print("--- Event-Driven Counter ---")

func createCounter(name) {
    counter := newEventEmitter()
    value := 0

    counter.increment = func(amount) {
        if amount == nil { amount = 1 }
        old := value
        value = value + amount
        counter.emit("change", {name: name, old: old, new: value, action: "increment"})
        if value > 10 {
            counter.emit("threshold", {name: name, value: value, threshold: 10})
        }
    }

    counter.decrement = func(amount) {
        if amount == nil { amount = 1 }
        old := value
        value = value - amount
        counter.emit("change", {name: name, old: old, new: value, action: "decrement"})
    }

    counter.getValue = func() { return value }

    return counter
}

c := createCounter("clicks")

// Listen for changes
c.on("change", func(data) {
    print(string.format("    [%s] %s: %d -> %d", data.name, data.action, data.old, data.new))
})

// Listen for threshold (once)
c.once("threshold", func(data) {
    print(string.format("    [%s] THRESHOLD EXCEEDED! Value %d > %d", data.name, data.value, data.threshold))
})

c.increment(5)
c.increment(3)
c.increment(4)  // This should trigger threshold
c.increment(2)  // Threshold already handled (once)
c.decrement(1)
print("  Final value:", c.getValue())
print()

// -------------------------------------------------------
// 7. Pub/Sub message broker
// -------------------------------------------------------
print("--- Pub/Sub Message Broker ---")

func newMessageBroker() {
    emitter := newEventEmitter()
    broker := {}
    messageCount := 0

    broker.subscribe = func(topic, subscriber) {
        emitter.on(topic, subscriber)
        print(string.format("    Subscribed to '%s'", topic))
    }

    broker.publish = func(topic, message) {
        messageCount = messageCount + 1
        print(string.format("    Publishing to '%s': %s", topic, tostring(message)))
        emitter.emit(topic, message)
    }

    broker.unsubscribe = func(topic, subscriber) {
        emitter.off(topic, subscriber)
    }

    broker.stats = func() {
        return messageCount
    }

    return broker
}

broker := newMessageBroker()

// Subscribers
broker.subscribe("news", func(msg) {
    print("      [Reader 1] Got news: " .. msg)
})

broker.subscribe("news", func(msg) {
    print("      [Reader 2] Got news: " .. msg)
})

broker.subscribe("sports", func(msg) {
    print("      [Sports Fan] " .. msg)
})

print()
broker.publish("news", "GScript 2.0 released!")
print()
broker.publish("sports", "Team GScript wins championship!")
print()
broker.publish("news", "New features announced")
print()
print("  Total messages published:", broker.stats())
print()

print("=== Done ===")
