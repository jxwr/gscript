// GScript Game Script
// Demonstrates the game engine embedding API

score := 0
frame := 0
enemies := {"enemy_1", "enemy_2", "enemy_3"}

func onStart() {
    engine.log("Game starting!")

    // Create player at center
    engine.createEntity("player", 400, 300)

    // Create enemies
    for i := 1; i <= 3; i++ {
        engine.createEntity("enemy_" .. i, i * 150, 100)
    }

    engine.log("Created " .. engine.entityCount() .. " entities")

    pos := engine.getEntityPos("player")
    engine.log("Player at: " .. pos.X .. ", " .. pos.Y)
}

func onUpdate(dt) {
    frame = frame + 1
    speed := 200.0

    // Movement
    move := Vec2.new(0, 0)
    if input.isDown("LEFT")  { move = Vec2.new(-1, 0) }
    if input.isDown("RIGHT") { move = Vec2.new(1, 0) }
    if input.isDown("UP")    { move = Vec2.new(0, -1) }
    if input.isDown("DOWN")  { move = Vec2.new(0, 1) }

    if move.Length() > 0 {
        move = move.Normalize()
        delta := move.Scale(speed * dt)
        pos := engine.getEntityPos("player")
        newPos := pos.Add(delta)
        engine.setEntityPos("player", newPos)
        engine.log("Frame " .. frame .. ": Player moved to " ..
            math.floor(newPos.X) .. ", " ..
            math.floor(newPos.Y))
    }

    // Check SPACE = attack
    if input.isDown("SPACE") {
        engine.log("Frame " .. frame .. ": ATTACK!")
        score = score + 10
    }

    // Move enemies toward player
    playerPos := engine.getEntityPos("player")
    for _, eName := range enemies {
        if !engine.isEntityActive(eName) { continue }

        ePos := engine.getEntityPos(eName)
        toPlayer := playerPos.Sub(ePos)
        dist := toPlayer.Length()

        if dist < 20 {
            engine.log("Enemy " .. eName .. " reached player!")
            engine.deactivateEntity(eName)
        } elseif dist > 0 {
            dir := toPlayer.Normalize()
            step := dir.Scale(80 * dt)
            engine.setEntityPos(eName, ePos.Add(step))
        }
    }

    engine.log("Frame " .. frame .. " | Score: " .. score ..
        " | Entities: " .. engine.entityCount())
}
