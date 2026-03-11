// ============================================================================
// GScript Chinese Chess (Xiangqi) - Complete game using rl.* raylib library
// ============================================================================

// === CONSTANTS ===
BOARD_X := 50
BOARD_Y := 60
CELL_SIZE := 65
PIECE_RADIUS := 26
WIN_W := 700
WIN_H := 720

// Colors (tables with r, g, b, a keys)
COLOR_BOARD_BG := {r: 220, g: 180, b: 120, a: 255}
COLOR_GRID := {r: 100, g: 60, b: 20, a: 255}
COLOR_RED_BG := {r: 200, g: 30, b: 30, a: 255}
COLOR_RED_BORDER := {r: 150, g: 10, b: 10, a: 255}
COLOR_BLACK_BG := {r: 30, g: 30, b: 30, a: 255}
COLOR_BLACK_BORDER := {r: 0, g: 0, b: 0, a: 255}
COLOR_SELECTED := {r: 255, g: 215, b: 0, a: 255}
COLOR_VALID_MOVE := {r: 50, g: 200, b: 50, a: 180}
COLOR_PIECE_TEXT_RED := {r: 255, g: 240, b: 220, a: 255}
COLOR_PIECE_TEXT_BLACK := {r: 200, g: 200, b: 200, a: 255}
COLOR_WHITE := {r: 255, g: 255, b: 255, a: 255}
COLOR_STATUS_BG := {r: 60, g: 40, b: 20, a: 255}
COLOR_LAST_MOVE := {r: 100, g: 180, b: 255, a: 120}

// === GAME STATE ===
board := {}          // board[col*100+row] = piece or nil
turn := "red"        // "red" or "black"
selectedPiece := nil // currently selected piece
validMoves := {}     // table of valid moves for selected piece
moveHistory := {}    // stack for undo
capturedRed := {}    // captured red pieces
capturedBlack := {}  // captured black pieces
gameStatus := ""     // "", "check", "redwin", "blackwin", "draw"
lastMoveFrom := nil  // {col, row} of last move origin
lastMoveTo := nil    // {col, row} of last move destination
fontLoaded := false
font := nil

// === PIECE HELPER ===
func makePiece(ptype, side, col, row) {
    return {type: ptype, side: side, col: col, row: row}
}

func boardKey(col, row) {
    return col * 100 + row
}

func getPiece(col, row) {
    if col < 1 || col > 9 || row < 1 || row > 10 {
        return nil
    }
    return board[boardKey(col, row)]
}

func setPiece(col, row, piece) {
    if piece != nil {
        piece.col = col
        piece.row = row
    }
    board[boardKey(col, row)] = piece
}

func removePiece(col, row) {
    board[boardKey(col, row)] = nil
}

// === BOARD INITIALIZATION ===
func initBoard() {
    board = {}
    turn = "red"
    selectedPiece = nil
    validMoves = {}
    moveHistory = {}
    capturedRed = {}
    capturedBlack = {}
    gameStatus = ""
    lastMoveFrom = nil
    lastMoveTo = nil

    // Red pieces (bottom, rows 1-5)
    setPiece(5, 1, makePiece("K", "red", 5, 1))
    setPiece(4, 1, makePiece("A", "red", 4, 1))
    setPiece(6, 1, makePiece("A", "red", 6, 1))
    setPiece(3, 1, makePiece("E", "red", 3, 1))
    setPiece(7, 1, makePiece("E", "red", 7, 1))
    setPiece(2, 1, makePiece("H", "red", 2, 1))
    setPiece(8, 1, makePiece("H", "red", 8, 1))
    setPiece(1, 1, makePiece("R", "red", 1, 1))
    setPiece(9, 1, makePiece("R", "red", 9, 1))
    setPiece(2, 3, makePiece("C", "red", 2, 3))
    setPiece(8, 3, makePiece("C", "red", 8, 3))
    setPiece(1, 4, makePiece("P", "red", 1, 4))
    setPiece(3, 4, makePiece("P", "red", 3, 4))
    setPiece(5, 4, makePiece("P", "red", 5, 4))
    setPiece(7, 4, makePiece("P", "red", 7, 4))
    setPiece(9, 4, makePiece("P", "red", 9, 4))

    // Black pieces (top, rows 6-10)
    setPiece(5, 10, makePiece("K", "black", 5, 10))
    setPiece(4, 10, makePiece("A", "black", 4, 10))
    setPiece(6, 10, makePiece("A", "black", 6, 10))
    setPiece(3, 10, makePiece("E", "black", 3, 10))
    setPiece(7, 10, makePiece("E", "black", 7, 10))
    setPiece(2, 10, makePiece("H", "black", 2, 10))
    setPiece(8, 10, makePiece("H", "black", 8, 10))
    setPiece(1, 10, makePiece("R", "black", 1, 10))
    setPiece(9, 10, makePiece("R", "black", 9, 10))
    setPiece(2, 8, makePiece("C", "black", 2, 8))
    setPiece(8, 8, makePiece("C", "black", 8, 8))
    setPiece(1, 7, makePiece("P", "black", 1, 7))
    setPiece(3, 7, makePiece("P", "black", 3, 7))
    setPiece(5, 7, makePiece("P", "black", 5, 7))
    setPiece(7, 7, makePiece("P", "black", 7, 7))
    setPiece(9, 7, makePiece("P", "black", 9, 7))
}

// === COORDINATE CONVERSION ===
func colToPixel(col) {
    return BOARD_X + (col - 1) * CELL_SIZE
}

func rowToPixel(row) {
    return BOARD_Y + (10 - row) * CELL_SIZE
}

func pixelToCell(px, py) {
    col := math.floor((px - BOARD_X + CELL_SIZE / 2) / CELL_SIZE) + 1
    rowFromTop := math.floor((py - BOARD_Y + CELL_SIZE / 2) / CELL_SIZE)
    row := 10 - rowFromTop
    if col < 1 || col > 9 || row < 1 || row > 10 {
        return nil, nil
    }
    return col, row
}

// === FIND GENERAL POSITION ===
func findGeneral(side) {
    for c := 1; c <= 9; c++ {
        for r := 1; r <= 10; r++ {
            p := getPiece(c, r)
            if p != nil && p.type == "K" && p.side == side {
                return c, r
            }
        }
    }
    return nil, nil
}

// === RAW MOVE GENERATION (without check filtering) ===
func getRawMoves(piece) {
    moves := {}
    c := piece.col
    r := piece.row
    side := piece.side
    ptype := piece.type

    if ptype == "K" {
        // General: 1 step orthogonally, must stay in palace
        dirs := {{0, 1}, {0, -1}, {1, 0}, {-1, 0}}
        for i := 1; i <= #dirs; i++ {
            nc := c + dirs[i][1]
            nr := r + dirs[i][2]
            // Check palace bounds
            inPalace := false
            if side == "red" && nc >= 4 && nc <= 6 && nr >= 1 && nr <= 3 {
                inPalace = true
            }
            if side == "black" && nc >= 4 && nc <= 6 && nr >= 8 && nr <= 10 {
                inPalace = true
            }
            if inPalace {
                target := getPiece(nc, nr)
                if target == nil || target.side != side {
                    table.insert(moves, {col: nc, row: nr})
                }
            }
        }
        // Flying general: can capture enemy general if on same column with no pieces between
        enemySide := "black"
        if side == "black" {
            enemySide = "red"
        }
        ec, er := findGeneral(enemySide)
        if ec != nil && ec == c {
            blocked := false
            minR := r
            maxR := er
            if minR > maxR {
                minR = er
                maxR = r
            }
            for checkR := minR + 1; checkR < maxR; checkR++ {
                if getPiece(c, checkR) != nil {
                    blocked = true
                    break
                }
            }
            if !blocked {
                table.insert(moves, {col: ec, row: er})
            }
        }
    }

    if ptype == "A" {
        // Advisor: 1 step diagonally, must stay in palace
        dirs := {{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
        for i := 1; i <= #dirs; i++ {
            nc := c + dirs[i][1]
            nr := r + dirs[i][2]
            inPalace := false
            if side == "red" && nc >= 4 && nc <= 6 && nr >= 1 && nr <= 3 {
                inPalace = true
            }
            if side == "black" && nc >= 4 && nc <= 6 && nr >= 8 && nr <= 10 {
                inPalace = true
            }
            if inPalace {
                target := getPiece(nc, nr)
                if target == nil || target.side != side {
                    table.insert(moves, {col: nc, row: nr})
                }
            }
        }
    }

    if ptype == "E" {
        // Elephant: 2 steps diagonally, cannot cross river, blocked at midpoint
        dirs := {{2, 2}, {2, -2}, {-2, 2}, {-2, -2}}
        blocks := {{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
        for i := 1; i <= #dirs; i++ {
            nc := c + dirs[i][1]
            nr := r + dirs[i][2]
            bc := c + blocks[i][1]
            br := r + blocks[i][2]
            if nc >= 1 && nc <= 9 && nr >= 1 && nr <= 10 {
                // Check river constraint
                validSide := false
                if side == "red" && nr >= 1 && nr <= 5 {
                    validSide = true
                }
                if side == "black" && nr >= 6 && nr <= 10 {
                    validSide = true
                }
                if validSide && getPiece(bc, br) == nil {
                    target := getPiece(nc, nr)
                    if target == nil || target.side != side {
                        table.insert(moves, {col: nc, row: nr})
                    }
                }
            }
        }
    }

    if ptype == "H" {
        // Horse: 1 orthogonal + 1 diagonal outward, blocked at orthogonal step
        // The 8 possible moves and their blocking squares
        horseMoves := {
            {dc: 1, dr: 2, bc: 0, br: 1},
            {dc: 1, dr: -2, bc: 0, br: -1},
            {dc: -1, dr: 2, bc: 0, br: 1},
            {dc: -1, dr: -2, bc: 0, br: -1},
            {dc: 2, dr: 1, bc: 1, br: 0},
            {dc: 2, dr: -1, bc: 1, br: 0},
            {dc: -2, dr: 1, bc: -1, br: 0},
            {dc: -2, dr: -1, bc: -1, br: 0}
        }
        for i := 1; i <= #horseMoves; i++ {
            hm := horseMoves[i]
            nc := c + hm.dc
            nr := r + hm.dr
            blockC := c + hm.bc
            blockR := r + hm.br
            if nc >= 1 && nc <= 9 && nr >= 1 && nr <= 10 {
                if getPiece(blockC, blockR) == nil {
                    target := getPiece(nc, nr)
                    if target == nil || target.side != side {
                        table.insert(moves, {col: nc, row: nr})
                    }
                }
            }
        }
    }

    if ptype == "R" {
        // Chariot: any number of steps orthogonally
        dirs := {{0, 1}, {0, -1}, {1, 0}, {-1, 0}}
        for i := 1; i <= #dirs; i++ {
            dc := dirs[i][1]
            dr := dirs[i][2]
            nc := c + dc
            nr := r + dr
            for nc >= 1 && nc <= 9 && nr >= 1 && nr <= 10 {
                target := getPiece(nc, nr)
                if target == nil {
                    table.insert(moves, {col: nc, row: nr})
                } else {
                    if target.side != side {
                        table.insert(moves, {col: nc, row: nr})
                    }
                    break
                }
                nc = nc + dc
                nr = nr + dr
            }
        }
    }

    if ptype == "C" {
        // Cannon: moves like chariot without capturing; captures by jumping over exactly one piece
        dirs := {{0, 1}, {0, -1}, {1, 0}, {-1, 0}}
        for i := 1; i <= #dirs; i++ {
            dc := dirs[i][1]
            dr := dirs[i][2]
            nc := c + dc
            nr := r + dr
            // Phase 1: move without capturing (no piece in the way)
            for nc >= 1 && nc <= 9 && nr >= 1 && nr <= 10 {
                target := getPiece(nc, nr)
                if target == nil {
                    table.insert(moves, {col: nc, row: nr})
                } else {
                    // Found the cannon platform, now look for capture target
                    nc = nc + dc
                    nr = nr + dr
                    for nc >= 1 && nc <= 9 && nr >= 1 && nr <= 10 {
                        target2 := getPiece(nc, nr)
                        if target2 != nil {
                            if target2.side != side {
                                table.insert(moves, {col: nc, row: nr})
                            }
                            break
                        }
                        nc = nc + dc
                        nr = nr + dr
                    }
                    break
                }
                nc = nc + dc
                nr = nr + dr
            }
        }
    }

    if ptype == "P" {
        // Pawn
        if side == "red" {
            // Red forward = increasing row
            crossedRiver := r >= 6
            // Forward
            if r + 1 <= 10 {
                target := getPiece(c, r + 1)
                if target == nil || target.side != side {
                    table.insert(moves, {col: c, row: r + 1})
                }
            }
            // After crossing river, can also go sideways
            if crossedRiver {
                if c - 1 >= 1 {
                    target := getPiece(c - 1, r)
                    if target == nil || target.side != side {
                        table.insert(moves, {col: c - 1, row: r})
                    }
                }
                if c + 1 <= 9 {
                    target := getPiece(c + 1, r)
                    if target == nil || target.side != side {
                        table.insert(moves, {col: c + 1, row: r})
                    }
                }
            }
        } else {
            // Black forward = decreasing row
            crossedRiver := r <= 5
            // Forward
            if r - 1 >= 1 {
                target := getPiece(c, r - 1)
                if target == nil || target.side != side {
                    table.insert(moves, {col: c, row: r - 1})
                }
            }
            // After crossing river, can also go sideways
            if crossedRiver {
                if c - 1 >= 1 {
                    target := getPiece(c - 1, r)
                    if target == nil || target.side != side {
                        table.insert(moves, {col: c - 1, row: r})
                    }
                }
                if c + 1 <= 9 {
                    target := getPiece(c + 1, r)
                    if target == nil || target.side != side {
                        table.insert(moves, {col: c + 1, row: r})
                    }
                }
            }
        }
    }

    return moves
}

// === CHECK DETECTION ===
// Check if the given side's general is under attack
func isInCheck(side) {
    gc, gr := findGeneral(side)
    if gc == nil {
        return true // General captured = in check
    }

    enemySide := "black"
    if side == "black" {
        enemySide = "red"
    }

    // Check all enemy pieces for ability to attack the general's position
    for ec := 1; ec <= 9; ec++ {
        for er := 1; er <= 10; er++ {
            ep := getPiece(ec, er)
            if ep != nil && ep.side == enemySide {
                rawMoves := getRawMoves(ep)
                for i := 1; i <= #rawMoves; i++ {
                    if rawMoves[i].col == gc && rawMoves[i].row == gr {
                        return true
                    }
                }
            }
        }
    }

    // Also check flying general rule
    enemyGC, enemyGR := findGeneral(enemySide)
    if enemyGC != nil && enemyGC == gc {
        blocked := false
        minR := gr
        maxR := enemyGR
        if minR > maxR {
            minR = enemyGR
            maxR = gr
        }
        for checkR := minR + 1; checkR < maxR; checkR++ {
            if getPiece(gc, checkR) != nil {
                blocked = true
                break
            }
        }
        if !blocked {
            return true
        }
    }

    return false
}

// === LEGAL MOVE FILTERING ===
// Get all legal moves for a piece (filtered: no self-check)
func getValidMovesList(piece) {
    rawMoves := getRawMoves(piece)
    legalMoves := {}

    fc := piece.col
    fr := piece.row
    side := piece.side

    for i := 1; i <= #rawMoves; i++ {
        tc := rawMoves[i].col
        tr := rawMoves[i].row

        // Simulate the move
        captured := getPiece(tc, tr)
        setPiece(tc, tr, piece)
        removePiece(fc, fr)

        // Check if own general is in check after the move
        inCheck := isInCheck(side)

        // Undo simulation
        setPiece(fc, fr, piece)
        if captured != nil {
            setPiece(tc, tr, captured)
        } else {
            removePiece(tc, tr)
        }

        if !inCheck {
            table.insert(legalMoves, {col: tc, row: tr})
        }
    }

    return legalMoves
}

// === CHECK IF ANY LEGAL MOVE EXISTS ===
func hasAnyLegalMove(side) {
    for c := 1; c <= 9; c++ {
        for r := 1; r <= 10; r++ {
            p := getPiece(c, r)
            if p != nil && p.side == side {
                lm := getValidMovesList(p)
                if #lm > 0 {
                    return true
                }
            }
        }
    }
    return false
}

// === MOVE EXECUTION ===
func doMove(piece, toCol, toRow) {
    fromCol := piece.col
    fromRow := piece.row
    captured := getPiece(toCol, toRow)

    // Save to history for undo
    histEntry := {
        ptype: piece.type,
        pside: piece.side,
        fromCol: fromCol,
        fromRow: fromRow,
        toCol: toCol,
        toRow: toRow,
        capturedType: nil,
        capturedSide: nil
    }
    if captured != nil {
        histEntry.capturedType = captured.type
        histEntry.capturedSide = captured.side
    }
    table.insert(moveHistory, histEntry)

    // Track captured pieces
    if captured != nil {
        if captured.side == "red" {
            table.insert(capturedRed, captured.type)
        } else {
            table.insert(capturedBlack, captured.type)
        }
    }

    // Execute move
    removePiece(fromCol, fromRow)
    setPiece(toCol, toRow, piece)

    // Track last move for highlighting
    lastMoveFrom = {col: fromCol, row: fromRow}
    lastMoveTo = {col: toCol, row: toRow}

    // Switch turn
    if turn == "red" {
        turn = "black"
    } else {
        turn = "red"
    }

    // Check game status
    checkGameStatus()
}

func checkGameStatus() {
    // Check if current player's general exists
    gc, gr := findGeneral(turn)
    if gc == nil {
        // General was captured, previous player wins
        if turn == "red" {
            gameStatus = "blackwin"
        } else {
            gameStatus = "redwin"
        }
        return
    }

    inCheck := isInCheck(turn)
    hasMove := hasAnyLegalMove(turn)

    if !hasMove {
        if inCheck {
            // Checkmate
            if turn == "red" {
                gameStatus = "blackwin"
            } else {
                gameStatus = "redwin"
            }
        } else {
            // Stalemate
            gameStatus = "draw"
        }
    } elseif inCheck {
        gameStatus = "check"
    } else {
        gameStatus = ""
    }
}

// === UNDO MOVE ===
func undoLastMove() {
    if #moveHistory == 0 {
        return
    }
    hist := moveHistory[#moveHistory]
    table.remove(moveHistory, #moveHistory)

    // Get the piece at destination
    piece := getPiece(hist.toCol, hist.toRow)
    if piece == nil {
        return
    }

    // Move piece back
    removePiece(hist.toCol, hist.toRow)
    setPiece(hist.fromCol, hist.fromRow, piece)

    // Restore captured piece if any
    if hist.capturedType != nil {
        restored := makePiece(hist.capturedType, hist.capturedSide, hist.toCol, hist.toRow)
        setPiece(hist.toCol, hist.toRow, restored)

        // Remove from captured list
        if hist.capturedSide == "red" {
            if #capturedRed > 0 {
                table.remove(capturedRed, #capturedRed)
            }
        } else {
            if #capturedBlack > 0 {
                table.remove(capturedBlack, #capturedBlack)
            }
        }
    }

    // Switch turn back
    if turn == "red" {
        turn = "black"
    } else {
        turn = "red"
    }

    // Reset selection
    selectedPiece = nil
    validMoves = {}

    // Update last move highlighting
    if #moveHistory > 0 {
        prevHist := moveHistory[#moveHistory]
        lastMoveFrom = {col: prevHist.fromCol, row: prevHist.fromRow}
        lastMoveTo = {col: prevHist.toCol, row: prevHist.toRow}
    } else {
        lastMoveFrom = nil
        lastMoveTo = nil
    }

    // Re-check game status
    inCheck := isInCheck(turn)
    if inCheck {
        gameStatus = "check"
    } else {
        gameStatus = ""
    }
}

// === PIECE DISPLAY NAMES ===
func getPieceLabel(piece) {
    if fontLoaded {
        if piece.side == "red" {
            if piece.type == "K" { return "帅" }
            if piece.type == "A" { return "仕" }
            if piece.type == "E" { return "相" }
            if piece.type == "H" { return "马" }
            if piece.type == "R" { return "车" }
            if piece.type == "C" { return "炮" }
            if piece.type == "P" { return "兵" }
        } else {
            if piece.type == "K" { return "将" }
            if piece.type == "A" { return "士" }
            if piece.type == "E" { return "象" }
            if piece.type == "H" { return "马" }
            if piece.type == "R" { return "车" }
            if piece.type == "C" { return "炮" }
            if piece.type == "P" { return "卒" }
        }
    }
    // ASCII fallback
    return piece.type
}

// === DRAWING FUNCTIONS ===

func drawBoard() {
    // Board background
    bw := 8 * CELL_SIZE
    bh := 9 * CELL_SIZE
    rl.drawRectangle(BOARD_X - 20, BOARD_Y - 20, bw + 40, bh + 40, COLOR_BOARD_BG)

    // Draw grid lines
    for c := 0; c <= 8; c++ {
        x := BOARD_X + c * CELL_SIZE
        // Top half (rows 10 to 6, pixel rows 0 to 4)
        rl.drawLine(x, BOARD_Y, x, BOARD_Y + 4 * CELL_SIZE, COLOR_GRID)
        // Bottom half (rows 5 to 1, pixel rows 5 to 9)
        rl.drawLine(x, BOARD_Y + 5 * CELL_SIZE, x, BOARD_Y + 9 * CELL_SIZE, COLOR_GRID)
    }

    for r := 0; r <= 9; r++ {
        y := BOARD_Y + r * CELL_SIZE
        rl.drawLine(BOARD_X, y, BOARD_X + 8 * CELL_SIZE, y, COLOR_GRID)
    }

    // Draw left and right border lines along the full height (river area)
    rl.drawLine(BOARD_X, BOARD_Y + 4 * CELL_SIZE, BOARD_X, BOARD_Y + 5 * CELL_SIZE, COLOR_GRID)
    rl.drawLine(BOARD_X + 8 * CELL_SIZE, BOARD_Y + 4 * CELL_SIZE, BOARD_X + 8 * CELL_SIZE, BOARD_Y + 5 * CELL_SIZE, COLOR_GRID)

    // Palace diagonals - top palace (black side, rows 8-10 -> pixel y: 0-2)
    px1 := BOARD_X + 3 * CELL_SIZE
    px2 := BOARD_X + 5 * CELL_SIZE
    py1 := BOARD_Y
    py2 := BOARD_Y + 2 * CELL_SIZE
    rl.drawLine(px1, py1, px2, py2, COLOR_GRID)
    rl.drawLine(px2, py1, px1, py2, COLOR_GRID)

    // Palace diagonals - bottom palace (red side, rows 1-3 -> pixel y: 7-9)
    py3 := BOARD_Y + 7 * CELL_SIZE
    py4 := BOARD_Y + 9 * CELL_SIZE
    rl.drawLine(px1, py3, px2, py4, COLOR_GRID)
    rl.drawLine(px2, py3, px1, py4, COLOR_GRID)

    // River label
    riverY := BOARD_Y + 4 * CELL_SIZE + CELL_SIZE / 2
    if fontLoaded {
        rl.drawTextEx(font, "楚河", BOARD_X + CELL_SIZE - 10, riverY - 15, 28, 2, COLOR_GRID)
        rl.drawTextEx(font, "汉界", BOARD_X + 5 * CELL_SIZE + 10, riverY - 15, 28, 2, COLOR_GRID)
    } else {
        rl.drawText("~~ RIVER ~~", BOARD_X + 2 * CELL_SIZE, riverY - 10, 20, COLOR_GRID)
    }
}

func drawLastMoveHighlight() {
    if lastMoveFrom != nil {
        px := colToPixel(lastMoveFrom.col)
        py := rowToPixel(lastMoveFrom.row)
        rl.drawRectangle(px - 15, py - 15, 30, 30, COLOR_LAST_MOVE)
    }
    if lastMoveTo != nil {
        px := colToPixel(lastMoveTo.col)
        py := rowToPixel(lastMoveTo.row)
        rl.drawRectangle(px - 15, py - 15, 30, 30, COLOR_LAST_MOVE)
    }
}

func drawPieces() {
    for c := 1; c <= 9; c++ {
        for r := 1; r <= 10; r++ {
            piece := getPiece(c, r)
            if piece != nil {
                px := colToPixel(c)
                py := rowToPixel(r)

                // Draw selected highlight
                if selectedPiece != nil && selectedPiece.col == c && selectedPiece.row == r {
                    rl.drawCircle(px, py, PIECE_RADIUS + 4, COLOR_SELECTED)
                }

                // Draw piece circle
                if piece.side == "red" {
                    rl.drawCircle(px, py, PIECE_RADIUS, COLOR_RED_BG)
                    rl.drawCircleLines(px, py, PIECE_RADIUS, COLOR_RED_BORDER)
                    // Inner ring
                    rl.drawCircleLines(px, py, PIECE_RADIUS - 3, COLOR_RED_BORDER)
                } else {
                    rl.drawCircle(px, py, PIECE_RADIUS, COLOR_BLACK_BG)
                    rl.drawCircleLines(px, py, PIECE_RADIUS, COLOR_BLACK_BORDER)
                    // Inner ring
                    rl.drawCircleLines(px, py, PIECE_RADIUS - 3, {r: 80, g: 80, b: 80, a: 255})
                }

                // Draw piece label
                label := getPieceLabel(piece)
                if fontLoaded {
                    tw, th := rl.measureTextEx(font, label, 26, 1)
                    tx := px - tw / 2
                    ty := py - th / 2
                    if piece.side == "red" {
                        rl.drawTextEx(font, label, tx, ty, 26, 1, COLOR_PIECE_TEXT_RED)
                    } else {
                        rl.drawTextEx(font, label, tx, ty, 26, 1, COLOR_PIECE_TEXT_BLACK)
                    }
                } else {
                    tw := rl.measureText(label, 24)
                    tx := px - tw / 2
                    ty := py - 12
                    if piece.side == "red" {
                        rl.drawText(label, tx, ty, 24, COLOR_PIECE_TEXT_RED)
                    } else {
                        rl.drawText(label, tx, ty, 24, COLOR_PIECE_TEXT_BLACK)
                    }
                }
            }
        }
    }
}

func drawValidMoveDots() {
    for i := 1; i <= #validMoves; i++ {
        mc := validMoves[i].col
        mr := validMoves[i].row
        px := colToPixel(mc)
        py := rowToPixel(mr)

        // If there's an enemy piece, draw a ring around it
        target := getPiece(mc, mr)
        if target != nil {
            rl.drawCircleLines(px, py, PIECE_RADIUS + 3, COLOR_VALID_MOVE)
            rl.drawCircleLines(px, py, PIECE_RADIUS + 4, COLOR_VALID_MOVE)
        } else {
            rl.drawCircle(px, py, 8, COLOR_VALID_MOVE)
        }
    }
}

func drawStatus() {
    // Status bar at top
    rl.drawRectangle(0, 0, WIN_W, 45, COLOR_STATUS_BG)

    statusText := ""
    statusColor := COLOR_WHITE

    if gameStatus == "redwin" {
        if fontLoaded {
            statusText = "红方胜!"
        } else {
            statusText = "RED WINS!"
        }
        statusColor = {r: 255, g: 100, b: 100, a: 255}
    } elseif gameStatus == "blackwin" {
        if fontLoaded {
            statusText = "黑方胜!"
        } else {
            statusText = "BLACK WINS!"
        }
        statusColor = {r: 180, g: 180, b: 255, a: 255}
    } elseif gameStatus == "draw" {
        if fontLoaded {
            statusText = "和棋!"
        } else {
            statusText = "DRAW!"
        }
        statusColor = {r: 255, g: 255, b: 100, a: 255}
    } elseif gameStatus == "check" {
        if fontLoaded {
            if turn == "red" {
                statusText = "红方走 - 将军!"
            } else {
                statusText = "黑方走 - 将军!"
            }
        } else {
            if turn == "red" {
                statusText = "RED's turn - CHECK!"
            } else {
                statusText = "BLACK's turn - CHECK!"
            }
        }
        statusColor = {r: 255, g: 200, b: 50, a: 255}
    } else {
        if fontLoaded {
            if turn == "red" {
                statusText = "红方走"
            } else {
                statusText = "黑方走"
            }
        } else {
            if turn == "red" {
                statusText = "RED's turn"
            } else {
                statusText = "BLACK's turn"
            }
        }
    }

    if fontLoaded {
        rl.drawTextEx(font, statusText, 15, 8, 28, 2, statusColor)
    } else {
        rl.drawText(statusText, 15, 12, 24, statusColor)
    }

    // Controls hint on the right side of status bar
    rl.drawText("R:Restart  U:Undo  ESC:Quit", 380, 15, 16, {r: 180, g: 180, b: 180, a: 255})
}

func drawCapturedPieces() {
    // Draw captured pieces on the right side
    rightX := BOARD_X + 8 * CELL_SIZE + 40
    capY := BOARD_Y + 10

    // Label
    rl.drawText("Captured:", rightX, capY, 16, {r: 180, g: 150, b: 100, a: 255})

    // Captured black pieces (by red)
    capY = capY + 25
    rl.drawText("By Red:", rightX, capY, 14, {r: 200, g: 100, b: 100, a: 255})
    capY = capY + 20
    for i := 1; i <= #capturedBlack; i++ {
        label := capturedBlack[i]
        rl.drawText(label, rightX + ((i - 1) % 4) * 20, capY + math.floor((i - 1) / 4) * 20, 16, {r: 100, g: 100, b: 100, a: 255})
    }

    capY = capY + math.floor((#capturedBlack + 3) / 4) * 20 + 20
    rl.drawText("By Black:", rightX, capY, 14, {r: 100, g: 100, b: 200, a: 255})
    capY = capY + 20
    for i := 1; i <= #capturedRed; i++ {
        label := capturedRed[i]
        rl.drawText(label, rightX + ((i - 1) % 4) * 20, capY + math.floor((i - 1) / 4) * 20, 16, {r: 200, g: 80, b: 80, a: 255})
    }
}

// === INPUT HANDLING ===
func handleClick(mx, my) {
    // Don't handle clicks if game is over
    if gameStatus == "redwin" || gameStatus == "blackwin" || gameStatus == "draw" {
        return
    }

    col, row := pixelToCell(mx, my)
    if col == nil {
        selectedPiece = nil
        validMoves = {}
        return
    }

    // If a piece is already selected, check if clicking a valid move destination
    if selectedPiece != nil {
        // Check if clicked on a valid move
        isValidDest := false
        for i := 1; i <= #validMoves; i++ {
            if validMoves[i].col == col && validMoves[i].row == row {
                isValidDest = true
                break
            }
        }
        if isValidDest {
            // Execute the move
            doMove(selectedPiece, col, row)
            selectedPiece = nil
            validMoves = {}
            return
        }
    }

    // Try to select a piece
    clicked := getPiece(col, row)
    if clicked != nil && clicked.side == turn {
        selectedPiece = clicked
        validMoves = getValidMovesList(clicked)
    } else {
        selectedPiece = nil
        validMoves = {}
    }
}

// === MAIN GAME ===
rl.initWindow(WIN_W, WIN_H, "Chinese Chess")
rl.setTargetFPS(60)

// Try to load a font that supports Chinese characters.
// Note: rl.loadFont only loads ASCII glyphs by default.
// Chinese character rendering requires loadFontEx with specific codepoints,
// which is not yet available. Set fontLoaded = false to use ASCII labels.
// If a future GScript version adds loadFontEx, enable this:
// font = rl.loadFont("/Library/Fonts/Arial Unicode.ttf")
// fontLoaded = true
fontLoaded = false

initBoard()

for !rl.windowShouldClose() {
    // Input handling
    if rl.isMouseButtonPressed(0) {
        handleClick(rl.getMouseX(), rl.getMouseY())
    }

    if rl.isKeyPressed(rl.KEY_R) {
        initBoard()
    }

    if rl.isKeyPressed(rl.KEY_U) {
        undoLastMove()
    }

    if rl.isKeyPressed(rl.KEY_ESCAPE) {
        break
    }

    // Rendering
    rl.beginDrawing()
    rl.clearBackground({r: 245, g: 235, b: 220, a: 255})

    drawBoard()
    drawLastMoveHighlight()
    drawPieces()
    drawValidMoveDots()
    drawStatus()
    drawCapturedPieces()

    rl.endDrawing()
}

rl.closeWindow()
