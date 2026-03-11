// ============================================================================
// GScript Tetris - A full-featured Tetris game using the gl stdlib
// ============================================================================

// --- Constants ---
BOARD_W := 10
BOARD_H := 20
CELL := 30
BOARD_X := 200
BOARD_Y := 40
WIN_W := 700
WIN_H := 720

// --- Piece definitions ---
// Each piece has a color {r, g, b} and 4 rotations.
// Each rotation is 4 cells as {row_offset, col_offset} from pivot.

PIECES := {
    // 1: I piece (cyan)
    {color: {0, 0.85, 0.85}, cells: {
        {{0, -1}, {0, 0}, {0, 1}, {0, 2}},
        {{-1, 0}, {0, 0}, {1, 0}, {2, 0}},
        {{0, -1}, {0, 0}, {0, 1}, {0, 2}},
        {{-1, 0}, {0, 0}, {1, 0}, {2, 0}}
    }},
    // 2: O piece (yellow)
    {color: {0.85, 0.85, 0}, cells: {
        {{0, 0}, {0, 1}, {1, 0}, {1, 1}},
        {{0, 0}, {0, 1}, {1, 0}, {1, 1}},
        {{0, 0}, {0, 1}, {1, 0}, {1, 1}},
        {{0, 0}, {0, 1}, {1, 0}, {1, 1}}
    }},
    // 3: T piece (purple)
    {color: {0.65, 0, 0.85}, cells: {
        {{0, -1}, {0, 0}, {0, 1}, {-1, 0}},
        {{-1, 0}, {0, 0}, {1, 0}, {0, 1}},
        {{0, -1}, {0, 0}, {0, 1}, {1, 0}},
        {{-1, 0}, {0, 0}, {1, 0}, {0, -1}}
    }},
    // 4: S piece (green)
    {color: {0, 0.85, 0}, cells: {
        {{0, 0}, {0, 1}, {-1, 1}, {-1, 2}},
        {{-1, 0}, {0, 0}, {0, 1}, {1, 1}},
        {{0, 0}, {0, 1}, {-1, 1}, {-1, 2}},
        {{-1, 0}, {0, 0}, {0, 1}, {1, 1}}
    }},
    // 5: Z piece (red)
    {color: {0.85, 0, 0}, cells: {
        {{-1, 0}, {-1, 1}, {0, 1}, {0, 2}},
        {{0, 0}, {1, 0}, {-1, 1}, {0, 1}},
        {{-1, 0}, {-1, 1}, {0, 1}, {0, 2}},
        {{0, 0}, {1, 0}, {-1, 1}, {0, 1}}
    }},
    // 6: J piece (blue)
    {color: {0, 0, 0.85}, cells: {
        {{-1, -1}, {0, -1}, {0, 0}, {0, 1}},
        {{-1, 0}, {-1, 1}, {0, 0}, {1, 0}},
        {{0, -1}, {0, 0}, {0, 1}, {1, 1}},
        {{-1, 0}, {0, 0}, {1, 0}, {1, -1}}
    }},
    // 7: L piece (orange)
    {color: {0.85, 0.4, 0}, cells: {
        {{0, -1}, {0, 0}, {0, 1}, {-1, 1}},
        {{-1, 0}, {0, 0}, {1, 0}, {1, 1}},
        {{0, -1}, {0, 0}, {0, 1}, {1, -1}},
        {{-1, 0}, {0, 0}, {1, 0}, {-1, -1}}
    }}
}

// --- Game State ---
board := {}
score := 0
level := 1
linesCleared := 0
gameOver := false
paused := false

// Current piece state
curType := 0
curRot := 1
curRow := 0
curCol := 0

// Next pieces queue (stores piece type indices)
nextQueue := {}

// Hold piece
holdType := 0
canHold := true

// 7-bag randomizer
bag := {}

// Drop timing
dropTimer := 0.0
softDropping := false

// --- Helper functions ---

func getDropInterval() {
    interval := 0.8 - (level - 1) * 0.07
    if interval < 0.1 {
        interval = 0.1
    }
    return interval
}

func shuffleBag() {
    bag = {1, 2, 3, 4, 5, 6, 7}
    // Fisher-Yates shuffle
    for i := 7; i >= 2; i-- {
        j := math.random(1, i)
        tmp := bag[i]
        bag[i] = bag[j]
        bag[j] = tmp
    }
}

bagIndex := 0

func nextFromBag() {
    if bagIndex >= #bag || #bag == 0 {
        shuffleBag()
        bagIndex = 0
    }
    bagIndex = bagIndex + 1
    return bag[bagIndex]
}

func fillQueue() {
    for #nextQueue < 3 {
        table.insert(nextQueue, nextFromBag())
    }
}

func initBoard() {
    board = {}
    for r := 1; r <= BOARD_H; r++ {
        board[r] = {}
        for c := 1; c <= BOARD_W; c++ {
            board[r][c] = 0
        }
    }
}

func getCells(pieceType, rotation) {
    return PIECES[pieceType].cells[rotation]
}

func getColor(pieceType) {
    return PIECES[pieceType].color
}

func isValid(pieceType, rotation, pRow, pCol) {
    cells := getCells(pieceType, rotation)
    for i := 1; i <= 4; i++ {
        r := pRow + cells[i][1]
        c := pCol + cells[i][2]
        if c < 1 || c > BOARD_W || r > BOARD_H {
            return false
        }
        // Allow pieces above the board (r < 1)
        if r >= 1 && board[r][c] != 0 {
            return false
        }
    }
    return true
}

func spawnPiece() {
    // Take from queue
    curType = nextQueue[1]
    table.remove(nextQueue, 1)
    fillQueue()

    curRot = 1
    curRow = 1
    curCol = math.floor(BOARD_W / 2)
    canHold = true
    dropTimer = 0.0

    if !isValid(curType, curRot, curRow, curCol) {
        gameOver = true
    }
}

func tryMove(dr, dc) {
    newRow := curRow + dr
    newCol := curCol + dc
    if isValid(curType, curRot, newRow, newCol) {
        curRow = newRow
        curCol = newCol
        return true
    }
    return false
}

func getGhostRow() {
    gr := curRow
    for isValid(curType, curRot, gr + 1, curCol) {
        gr = gr + 1
    }
    return gr
}

func lockPiece() {
    cells := getCells(curType, curRot)
    for i := 1; i <= 4; i++ {
        r := curRow + cells[i][1]
        c := curCol + cells[i][2]
        if r >= 1 && r <= BOARD_H && c >= 1 && c <= BOARD_W {
            board[r][c] = curType
        }
    }
    clearLines()
    spawnPiece()
}

func clearLines() {
    cleared := 0
    r := BOARD_H
    for r >= 1 {
        full := true
        for c := 1; c <= BOARD_W; c++ {
            if board[r][c] == 0 {
                full = false
                break
            }
        }
        if full {
            // Remove this row and shift everything above down
            table.remove(board, r)
            // Insert empty row at top
            newRow := {}
            for c := 1; c <= BOARD_W; c++ {
                newRow[c] = 0
            }
            table.insert(board, 1, newRow)
            cleared = cleared + 1
            // Don't decrement r since the row above shifted into this slot
        } else {
            r = r - 1
        }
    }

    if cleared > 0 {
        // Score: 1=100, 2=300, 3=500, 4=800
        lineScores := {100, 300, 500, 800}
        if cleared <= 4 {
            score = score + lineScores[cleared] * level
        } else {
            score = score + 800 * level
        }
        linesCleared = linesCleared + cleared
        level = math.floor(linesCleared / 10) + 1
    }
}

func rotatePiece(dir) {
    newRot := curRot + dir
    if newRot < 1 {
        newRot = 4
    }
    if newRot > 4 {
        newRot = 1
    }

    // Wall kick offsets to try: {dRow, dCol}
    kicks := {{0, 0}, {0, -1}, {0, 1}, {-1, 0}, {0, -2}, {0, 2}}
    for i := 1; i <= #kicks; i++ {
        nr := curRow + kicks[i][1]
        nc := curCol + kicks[i][2]
        if isValid(curType, newRot, nr, nc) {
            curRot = newRot
            curRow = nr
            curCol = nc
            return true
        }
    }
    return false
}

func hardDrop() {
    dropDist := 0
    for isValid(curType, curRot, curRow + 1, curCol) {
        curRow = curRow + 1
        dropDist = dropDist + 1
    }
    score = score + dropDist * 2
    lockPiece()
}

func holdCurrentPiece() {
    if !canHold {
        return
    }
    canHold = false
    if holdType == 0 {
        holdType = curType
        spawnPiece()
    } else {
        tmp := curType
        curType = holdType
        holdType = tmp
        curRot = 1
        curRow = 1
        curCol = math.floor(BOARD_W / 2)
        dropTimer = 0.0
        if !isValid(curType, curRot, curRow, curCol) {
            gameOver = true
        }
    }
}

// --- Drawing functions ---

func drawCell(col, row, r, g, b, a) {
    x := BOARD_X + (col - 1) * CELL
    y := BOARD_Y + (row - 1) * CELL
    // Main cell with 1px gap for grid effect
    gl.drawRect(x + 1, y + 1, CELL - 2, CELL - 2, r, g, b, a)
    // Highlight (top-left lighter edge)
    gl.drawRect(x + 1, y + 1, CELL - 2, 2, r + 0.15, g + 0.15, b + 0.15, a * 0.5)
    gl.drawRect(x + 1, y + 1, 2, CELL - 2, r + 0.15, g + 0.15, b + 0.15, a * 0.5)
}

func drawPieceAt(pieceType, rotation, pRow, pCol, alpha) {
    cells := getCells(pieceType, rotation)
    color := getColor(pieceType)
    for i := 1; i <= 4; i++ {
        r := pRow + cells[i][1]
        c := pCol + cells[i][2]
        if r >= 1 && r <= BOARD_H && c >= 1 && c <= BOARD_W {
            drawCell(c, r, color[1], color[2], color[3], alpha)
        }
    }
}

func drawMiniPiece(pieceType, px, py, cellSize) {
    cells := getCells(pieceType, 1)
    color := getColor(pieceType)
    for i := 1; i <= 4; i++ {
        dr := cells[i][1]
        dc := cells[i][2]
        x := px + dc * cellSize
        y := py + dr * cellSize
        gl.drawRect(x + 1, y + 1, cellSize - 2, cellSize - 2, color[1], color[2], color[3], 1)
    }
}

func drawBoard() {
    // Background
    gl.drawRect(BOARD_X - 2, BOARD_Y - 2, BOARD_W * CELL + 4, BOARD_H * CELL + 4, 0.12, 0.12, 0.15, 1)

    // Border
    gl.drawRectOutline(BOARD_X - 2, BOARD_Y - 2, BOARD_W * CELL + 4, BOARD_H * CELL + 4, 0.4, 0.4, 0.5, 2)

    // Grid lines
    for r := 0; r <= BOARD_H; r++ {
        gl.drawRect(BOARD_X, BOARD_Y + r * CELL, BOARD_W * CELL, 1, 0.2, 0.2, 0.25, 0.5)
    }
    for c := 0; c <= BOARD_W; c++ {
        gl.drawRect(BOARD_X + c * CELL, BOARD_Y, 1, BOARD_H * CELL, 0.2, 0.2, 0.25, 0.5)
    }

    // Locked cells
    for r := 1; r <= BOARD_H; r++ {
        for c := 1; c <= BOARD_W; c++ {
            if board[r][c] != 0 {
                color := getColor(board[r][c])
                drawCell(c, r, color[1], color[2], color[3], 1)
            }
        }
    }

    if !gameOver && curType > 0 {
        // Ghost piece
        ghostRow := getGhostRow()
        if ghostRow != curRow {
            drawPieceAt(curType, curRot, ghostRow, curCol, 0.25)
        }

        // Current piece
        drawPieceAt(curType, curRot, curRow, curCol, 1)
    }
}

func drawUI() {
    rightX := BOARD_X + BOARD_W * CELL + 30

    // NEXT label
    gl.drawText("NEXT", rightX, BOARD_Y, 2, 0.8, 0.8, 0.8)
    for i := 1; i <= #nextQueue; i++ {
        miniY := BOARD_Y + 30 + (i - 1) * 60
        drawMiniPiece(nextQueue[i], rightX + 10, miniY, 18)
    }

    // HOLD label
    leftX := 20
    gl.drawText("HOLD", leftX, BOARD_Y, 2, 0.8, 0.8, 0.8)
    if holdType > 0 {
        drawMiniPiece(holdType, leftX + 10, BOARD_Y + 30, 18)
    }

    // Score
    gl.drawText("SCORE", leftX, BOARD_Y + 120, 2, 0.8, 0.8, 0.8)
    gl.drawText(tostring(score), leftX, BOARD_Y + 150, 2, 1, 1, 1)

    // Level
    gl.drawText("LEVEL", leftX, BOARD_Y + 200, 2, 0.8, 0.8, 0.8)
    gl.drawText(tostring(level), leftX, BOARD_Y + 230, 2, 1, 1, 1)

    // Lines
    gl.drawText("LINES", leftX, BOARD_Y + 280, 2, 0.8, 0.8, 0.8)
    gl.drawText(tostring(linesCleared), leftX, BOARD_Y + 310, 2, 1, 1, 1)

    // Controls
    gl.drawText("CONTROLS", leftX, BOARD_Y + 400, 1.5, 0.6, 0.6, 0.6)
    gl.drawText("LEFT/RIGHT Move", leftX, BOARD_Y + 425, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("UP/X  Rotate CW", leftX, BOARD_Y + 445, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("Z     Rotate CCW", leftX, BOARD_Y + 465, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("DOWN  Soft drop", leftX, BOARD_Y + 485, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("SPACE Hard drop", leftX, BOARD_Y + 505, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("C     Hold", leftX, BOARD_Y + 525, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("P     Pause", leftX, BOARD_Y + 545, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("R     Restart", leftX, BOARD_Y + 565, 1.3, 0.5, 0.5, 0.5)
    gl.drawText("ESC   Quit", leftX, BOARD_Y + 585, 1.3, 0.5, 0.5, 0.5)
}

func drawGameOver() {
    // Dark overlay
    gl.drawRect(BOARD_X, BOARD_Y + BOARD_H * CELL / 2 - 40, BOARD_W * CELL, 80, 0, 0, 0, 0.8)
    gl.drawText("GAME OVER", BOARD_X + 60, BOARD_Y + BOARD_H * CELL / 2 - 25, 3, 1, 0.2, 0.2)
    gl.drawText("Press R to restart", BOARD_X + 55, BOARD_Y + BOARD_H * CELL / 2 + 15, 1.8, 0.8, 0.8, 0.8)
}

func drawPaused() {
    gl.drawRect(BOARD_X, BOARD_Y + BOARD_H * CELL / 2 - 30, BOARD_W * CELL, 60, 0, 0, 0, 0.7)
    gl.drawText("PAUSED", BOARD_X + 90, BOARD_Y + BOARD_H * CELL / 2 - 15, 3, 1, 1, 0.3)
}

// --- Game initialization ---

func initGame() {
    initBoard()
    score = 0
    level = 1
    linesCleared = 0
    gameOver = false
    paused = false
    holdType = 0
    canHold = true
    curType = 0
    curRot = 1
    curRow = 1
    curCol = math.floor(BOARD_W / 2)
    nextQueue = {}
    bag = {}
    bagIndex = 0
    dropTimer = 0.0

    fillQueue()
    spawnPiece()
}

// --- Main ---

win := gl.newWindow(WIN_W, WIN_H, "GScript Tetris")

initGame()

lastTime := gl.getTime()

for !win.shouldClose() {
    now := gl.getTime()
    dt := now - lastTime
    lastTime = now

    win.pollEvents()

    // --- Input handling ---
    if gl.isKeyJustPressed(gl.KEY_ESCAPE) {
        break
    }

    if gl.isKeyJustPressed(gl.KEY_R) {
        initGame()
    }

    if gl.isKeyJustPressed(gl.KEY_P) {
        if !gameOver {
            paused = !paused
        }
    }

    if !gameOver && !paused {
        // Move left/right
        if gl.isKeyJustPressed(gl.KEY_LEFT) {
            tryMove(0, -1)
        }
        if gl.isKeyJustPressed(gl.KEY_RIGHT) {
            tryMove(0, 1)
        }

        // Rotate
        if gl.isKeyJustPressed(gl.KEY_UP) || gl.isKeyJustPressed(gl.KEY_X) {
            rotatePiece(1)
        }
        if gl.isKeyJustPressed(gl.KEY_Z) {
            rotatePiece(-1)
        }

        // Hard drop
        if gl.isKeyJustPressed(gl.KEY_SPACE) {
            hardDrop()
        }

        // Hold
        if gl.isKeyJustPressed(gl.KEY_C) {
            holdCurrentPiece()
        }

        // Determine effective drop interval
        effectiveInterval := getDropInterval()
        if gl.isKeyDown(gl.KEY_DOWN) {
            effectiveInterval = 0.05
            // Soft drop score: add 1 per cell moved by auto-drop
        }

        // Auto drop
        dropTimer = dropTimer + dt
        if dropTimer >= effectiveInterval {
            dropTimer = 0.0
            if !tryMove(1, 0) {
                lockPiece()
            } else {
                // Soft drop score
                if gl.isKeyDown(gl.KEY_DOWN) {
                    score = score + 1
                }
            }
        }
    }

    // --- Render ---
    win.clear(0.05, 0.05, 0.1)

    drawBoard()
    drawUI()

    if gameOver {
        drawGameOver()
    }
    if paused && !gameOver {
        drawPaused()
    }

    win.swapBuffers()
}

win.close()
