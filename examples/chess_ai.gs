// ============================================================================
// GScript Chinese Chess (Xiangqi) with AI - Negamax + Alpha-Beta + Iterative Deepening
// Player = Red (bottom), AI = Black (top)
// ============================================================================

// === CONSTANTS ===
BOARD_X := 50
BOARD_Y := 90
CELL_SIZE := 65
PIECE_RADIUS := 26
WIN_W := 950
WIN_H := 760

// Button layout (bottom of window)
BTN_Y  := 710
BTN_H  := 38
BTN1_X := 140   // 重新开始
BTN1_W := 180
BTN2_X := 360   // 悔棋
BTN2_W := 120

// Colors
COLOR_BOARD_BG    := {r: 210, g: 165, b: 90, a: 255}
COLOR_GRID        := {r: 90,  g: 50,  b: 10, a: 255}
COLOR_RED_BG      := {r: 185, g: 25,  b: 25, a: 255}
COLOR_RED_BORDER  := {r: 230, g: 180, b: 120, a: 255}
COLOR_BLACK_BG    := {r: 25,  g: 25,  b: 25, a: 255}
COLOR_BLACK_BORDER := {r: 80, g: 80,  b: 80, a: 255}
COLOR_SELECTED    := {r: 255, g: 210, b: 0,   a: 255}
COLOR_VALID_MOVE  := {r: 60,  g: 210, b: 80,  a: 190}
COLOR_PIECE_TEXT_RED   := {r: 255, g: 235, b: 190, a: 255}
COLOR_PIECE_TEXT_BLACK := {r: 210, g: 210, b: 210, a: 255}
COLOR_WHITE       := {r: 255, g: 255, b: 255, a: 255}
COLOR_STATUS_BG   := {r: 45,  g: 28,  b: 10, a: 255}
COLOR_LAST_MOVE   := {r: 80,  g: 160, b: 255, a: 100}

// === GAME STATE ===
board := {}
turn := "red"
selectedPiece := nil
validMoves := {}
moveHistory := {}
capturedRed := {}
capturedBlack := {}
gameStatus := ""
lastMoveFrom := nil
lastMoveTo := nil
fontLoaded := false
font := nil
aiThinking := false
lastAIDepth := 0
aiCoroutine := nil
frameCount := 0

// Animation state
animActive := false
animFrame := 0
animDuration := 14        // frames (~0.23s at 60fps)
animFromX := 0
animFromY := 0
animToX := 0
animToY := 0
animPieceType := ""
animPieceSide := ""
animPieceCol := 0         // destination col (piece is already placed here in board)
animPieceRow := 0         // destination row
animPendingAI := false    // trigger AI after animation ends

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
    aiCoroutine = nil
    aiThinking = false
    lastAIDepth = 0

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
        dirs := {{0, 1}, {0, -1}, {1, 0}, {-1, 0}}
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
        // Flying general
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
        dirs := {{2, 2}, {2, -2}, {-2, 2}, {-2, -2}}
        blocks := {{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
        for i := 1; i <= #dirs; i++ {
            nc := c + dirs[i][1]
            nr := r + dirs[i][2]
            bc := c + blocks[i][1]
            br := r + blocks[i][2]
            if nc >= 1 && nc <= 9 && nr >= 1 && nr <= 10 {
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
        if side == "red" {
            crossedRiver := r >= 6
            if r + 1 <= 10 {
                target := getPiece(c, r + 1)
                if target == nil || target.side != side {
                    table.insert(moves, {col: c, row: r + 1})
                }
            }
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
            crossedRiver := r <= 5
            if r - 1 >= 1 {
                target := getPiece(c, r - 1)
                if target == nil || target.side != side {
                    table.insert(moves, {col: c, row: r - 1})
                }
            }
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
func isInCheck(side) {
    gc, gr := findGeneral(side)
    if gc == nil {
        return true
    }

    enemySide := "black"
    if side == "black" {
        enemySide = "red"
    }

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

    // Flying general check
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

    // Start animation
    startMoveAnim(fromCol, fromRow, toCol, toRow, piece.type, piece.side)

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

    if captured != nil {
        if captured.side == "red" {
            table.insert(capturedRed, captured.type)
        } else {
            table.insert(capturedBlack, captured.type)
        }
    }

    removePiece(fromCol, fromRow)
    setPiece(toCol, toRow, piece)

    lastMoveFrom = {col: fromCol, row: fromRow}
    lastMoveTo = {col: toCol, row: toRow}

    if turn == "red" {
        turn = "black"
    } else {
        turn = "red"
    }

    checkGameStatus()
}

func checkGameStatus() {
    gc, gr := findGeneral(turn)
    if gc == nil {
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
            if turn == "red" {
                gameStatus = "blackwin"
            } else {
                gameStatus = "redwin"
            }
        } else {
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

    piece := getPiece(hist.toCol, hist.toRow)
    if piece == nil {
        return
    }

    removePiece(hist.toCol, hist.toRow)
    setPiece(hist.fromCol, hist.fromRow, piece)

    if hist.capturedType != nil {
        restored := makePiece(hist.capturedType, hist.capturedSide, hist.toCol, hist.toRow)
        setPiece(hist.toCol, hist.toRow, restored)

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

    if turn == "red" {
        turn = "black"
    } else {
        turn = "red"
    }

    selectedPiece = nil
    validMoves = {}

    if #moveHistory > 0 {
        prevHist := moveHistory[#moveHistory]
        lastMoveFrom = {col: prevHist.fromCol, row: prevHist.fromRow}
        lastMoveTo = {col: prevHist.toCol, row: prevHist.toRow}
    } else {
        lastMoveFrom = nil
        lastMoveTo = nil
    }

    inCheck := isInCheck(turn)
    if inCheck {
        gameStatus = "check"
    } else {
        gameStatus = ""
    }
}

// ============================================================================
// === AI ENGINE ===
// ============================================================================

// === PIECE VALUES ===
func pieceValue(ptype) {
    if ptype == "K" { return 10000 }
    if ptype == "R" { return 900 }
    if ptype == "C" { return 450 }
    if ptype == "H" { return 400 }
    if ptype == "E" { return 200 }
    if ptype == "A" { return 200 }
    if ptype == "P" { return 100 }
    return 0
}

// === POSITION SQUARE TABLE (PST) ===
// Returns a positional bonus for a piece at a given position.
// col: 1-9, row: 1-10
// For red, row 1 is bottom (home). For black, we mirror: effective row = 11 - row.
func getPST(ptype, side, col, row) {
    // Normalize row to "own perspective" (row 1 = home side)
    r := row
    if side == "black" {
        r = 11 - row
    }

    if ptype == "R" {
        // Chariot: center file bonus
        bonus := 0
        if col == 5 {
            bonus = bonus + 4
        }
        if col == 4 || col == 6 {
            bonus = bonus + 2
        }
        // Slight bonus for being in enemy half
        if r >= 6 {
            bonus = bonus + 2
        }
        return bonus
    }

    if ptype == "H" {
        // Horse: center is best, corners/edges penalized
        bonus := 0
        // Corner penalty
        if (col == 1 || col == 9) && (r == 1 || r == 10) {
            return -10
        }
        // Edge penalty
        if col == 1 || col == 9 || r == 1 || r == 10 {
            return -4
        }
        // Central 3x3 (cols 4-6, rows 4-7 in own perspective)
        if col >= 4 && col <= 6 && r >= 4 && r <= 7 {
            return 16
        }
        // Wider center region (cols 3-7, rows 3-8)
        if col >= 3 && col <= 7 && r >= 3 && r <= 8 {
            return 8
        }
        return bonus
    }

    if ptype == "C" {
        // Cannon
        bonus := 0
        // Back rank starting positions
        if r == 1 && (col == 2 || col == 8) {
            bonus = bonus + 4
        }
        // Center file
        if col == 5 {
            bonus = bonus + 6
        }
        // Enemy half
        if r >= 6 {
            bonus = bonus + 8
        }
        return bonus
    }

    if ptype == "P" {
        // Pawn: no bonus before river, big bonus after
        if r <= 5 {
            return 0
        }
        // After river
        bonus := 50
        // Additional bonus per row advanced past river (row 6 = just crossed)
        bonus = bonus + (r - 5) * 10
        // Center columns bonus
        if col >= 4 && col <= 6 {
            bonus = bonus + 10
        }
        return bonus
    }

    if ptype == "A" {
        // Advisor: slight bonus for palace center
        if col == 5 && r == 2 {
            return 4
        }
        return 0
    }

    if ptype == "E" {
        // Elephant: slight bonus for good defensive spots
        // Standard good positions: (3,1), (7,1), (5,3), (1,3), (9,3), (3,5), (7,5)
        if r == 3 && (col == 1 || col == 5 || col == 9) {
            return 4
        }
        if r == 5 && (col == 3 || col == 7) {
            return 2
        }
        return 0
    }

    if ptype == "K" {
        // King: center of palace is slightly better
        if col == 5 && r == 2 {
            return 4
        }
        return 0
    }

    return 0
}

// === EVALUATION FUNCTION ===
// Returns score from Red's perspective (positive = Red winning)
func evaluateBoard() {
    // Check if generals exist
    redGC, redGR := findGeneral("red")
    blackGC, blackGR := findGeneral("black")

    if redGC == nil {
        return -99999
    }
    if blackGC == nil {
        return 99999
    }

    score := 0

    for c := 1; c <= 9; c++ {
        for r := 1; r <= 10; r++ {
            p := getPiece(c, r)
            if p != nil {
                pv := pieceValue(p.type)
                pst := getPST(p.type, p.side, c, r)

                // Pawn value adjustment: after river = 200 instead of 100
                if p.type == "P" {
                    if p.side == "red" && r >= 6 {
                        pv = 200
                    }
                    if p.side == "black" && r <= 5 {
                        pv = 200
                    }
                }

                if p.side == "red" {
                    score = score + pv + pst
                } else {
                    score = score - pv - pst
                }
            }
        }
    }

    return score
}

// === GET ALL MOVES FOR A SIDE ===
// Returns list of {piece, col, row}
func getAllMovesForSide(side) {
    allMoves := {}
    for c := 1; c <= 9; c++ {
        for r := 1; r <= 10; r++ {
            p := getPiece(c, r)
            if p != nil && p.side == side {
                legalMoves := getValidMovesList(p)
                for j := 1; j <= #legalMoves; j++ {
                    table.insert(allMoves, {piece: p, col: legalMoves[j].col, row: legalMoves[j].row})
                }
            }
        }
    }
    return allMoves
}

// === MOVE ORDERING ===
// Sort moves: captures first (by captured piece value descending), then non-captures
func orderMoves(moveList) {
    captures := {}
    nonCaptures := {}

    for i := 1; i <= #moveList; i++ {
        m := moveList[i]
        target := getPiece(m.col, m.row)
        if target != nil && target.side != m.piece.side {
            // Capture move — store with value for sorting
            table.insert(captures, {piece: m.piece, col: m.col, row: m.row, captVal: pieceValue(target.type)})
        } else {
            table.insert(nonCaptures, {piece: m.piece, col: m.col, row: m.row, captVal: 0})
        }
    }

    // Simple insertion sort on captures by captVal descending
    for i := 2; i <= #captures; i++ {
        j := i
        for j > 1 && captures[j].captVal > captures[j - 1].captVal {
            tmp := captures[j]
            captures[j] = captures[j - 1]
            captures[j - 1] = tmp
            j = j - 1
        }
    }

    // Build ordered list: captures first, then non-captures
    ordered := {}
    for i := 1; i <= #captures; i++ {
        table.insert(ordered, captures[i])
    }
    for i := 1; i <= #nonCaptures; i++ {
        table.insert(ordered, nonCaptures[i])
    }

    return ordered
}

// === NEGAMAX WITH ALPHA-BETA ===
func negamax(depth, alpha, beta, side) {
    if depth == 0 {
        score := evaluateBoard()
        if side == "red" {
            return score
        } else {
            return -score
        }
    }

    allMoves := getAllMovesForSide(side)

    if #allMoves == 0 {
        // No legal moves = loss (checkmate or stalemate treated as loss)
        return -99000
    }

    // Order moves for better pruning
    allMoves = orderMoves(allMoves)

    enemySide := "black"
    if side == "black" {
        enemySide = "red"
    }

    for i := 1; i <= #allMoves; i++ {
        m := allMoves[i]
        p := m.piece
        tc := m.col
        tr := m.row
        fc := p.col
        fr := p.row

        // Simulate move
        captured := getPiece(tc, tr)
        origCol := p.col
        origRow := p.row

        // Remove piece from source, place at destination
        removePiece(fc, fr)
        // We must NOT call setPiece here because it updates p.col/p.row and we
        // need to manage that ourselves for proper undo
        board[boardKey(tc, tr)] = p
        p.col = tc
        p.row = tr

        // Recurse
        score := -negamax(depth - 1, -beta, -alpha, enemySide)

        // Undo move
        board[boardKey(tc, tr)] = nil
        p.col = origCol
        p.row = origRow
        board[boardKey(fc, fr)] = p
        if captured != nil {
            board[boardKey(tc, tr)] = captured
            captured.col = tc
            captured.row = tr
        }

        if score >= beta {
            return beta
        }
        if score > alpha {
            alpha = score
        }
    }

    return alpha
}

// === GET AI MOVE (Iterative Deepening) ===
func getAIMove() {
    startTime := time.now()
    bestMove := nil
    lastAIDepth = 0

    for depth := 1; depth <= 6; depth++ {
        alpha := -999999
        beta := 999999
        localBest := nil

        allMoves := getAllMovesForSide("black")
        allMoves = orderMoves(allMoves)

        if #allMoves == 0 {
            break
        }

        for i := 1; i <= #allMoves; i++ {
            m := allMoves[i]
            p := m.piece
            tc := m.col
            tr := m.row
            fc := p.col
            fr := p.row

            // Simulate move
            captured := getPiece(tc, tr)
            origCol := p.col
            origRow := p.row

            removePiece(fc, fr)
            board[boardKey(tc, tr)] = p
            p.col = tc
            p.row = tr

            // Search from red's perspective (enemy of black)
            score := -negamax(depth - 1, -beta, -alpha, "red")

            // Undo move
            board[boardKey(tc, tr)] = nil
            p.col = origCol
            p.row = origRow
            board[boardKey(fc, fr)] = p
            if captured != nil {
                board[boardKey(tc, tr)] = captured
                captured.col = tc
                captured.row = tr
            }

            if score > alpha {
                alpha = score
                localBest = {piece: p, col: tc, row: tr}
            }
        }

        if localBest != nil {
            bestMove = localBest
        }
        lastAIDepth = depth

        // Yield once per depth level to keep UI alive
        coroutine.yield(nil)

        // Check time limit
        elapsed := time.since(startTime)
        if elapsed > 5.0 {
            break
        }

        // Found checkmate, no need to search deeper
        if alpha >= 99000 {
            break
        }
    }

    return bestMove
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
    return piece.type
}

// === DRAWING FUNCTIONS ===

// Soldier position mark (cross-hair notch)
markColor := {r: 90, g: 50, b: 10, a: 200}
markLen := 5
markGap := 3
func drawMark(mx, my) {
    rl.drawLine(mx - markLen - markGap, my, mx - markGap, my, markColor)
    rl.drawLine(mx + markGap, my, mx + markLen + markGap, my, markColor)
    rl.drawLine(mx, my - markLen - markGap, mx, my - markGap, markColor)
    rl.drawLine(mx, my + markGap, mx, my + markLen + markGap, markColor)
}

func drawBoard() {
    bw := 8 * CELL_SIZE
    bh := 9 * CELL_SIZE
    rl.drawRectangle(BOARD_X - 20, BOARD_Y - 20, bw + 40, bh + 40, COLOR_BOARD_BG)

    for c := 0; c <= 8; c++ {
        x := BOARD_X + c * CELL_SIZE
        rl.drawLine(x, BOARD_Y, x, BOARD_Y + 4 * CELL_SIZE, COLOR_GRID)
        rl.drawLine(x, BOARD_Y + 5 * CELL_SIZE, x, BOARD_Y + 9 * CELL_SIZE, COLOR_GRID)
    }

    for r := 0; r <= 9; r++ {
        y := BOARD_Y + r * CELL_SIZE
        rl.drawLine(BOARD_X, y, BOARD_X + 8 * CELL_SIZE, y, COLOR_GRID)
    }

    // River border lines
    rl.drawLine(BOARD_X, BOARD_Y + 4 * CELL_SIZE, BOARD_X, BOARD_Y + 5 * CELL_SIZE, COLOR_GRID)
    rl.drawLine(BOARD_X + 8 * CELL_SIZE, BOARD_Y + 4 * CELL_SIZE, BOARD_X + 8 * CELL_SIZE, BOARD_Y + 5 * CELL_SIZE, COLOR_GRID)

    // Palace diagonals - top palace (black side)
    px1 := BOARD_X + 3 * CELL_SIZE
    px2 := BOARD_X + 5 * CELL_SIZE
    py1 := BOARD_Y
    py2 := BOARD_Y + 2 * CELL_SIZE
    rl.drawLine(px1, py1, px2, py2, COLOR_GRID)
    rl.drawLine(px2, py1, px1, py2, COLOR_GRID)

    // Palace diagonals - bottom palace (red side)
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

    // Soldier position marks (standard cross-hair notches at cannon and pawn spots)
    // Cannon positions (col 2,8  row 3,8)
    drawMark(colToPixel(2), rowToPixel(3))
    drawMark(colToPixel(8), rowToPixel(3))
    drawMark(colToPixel(2), rowToPixel(8))
    drawMark(colToPixel(8), rowToPixel(8))
    // Pawn positions
    pawnCols := {1, 3, 5, 7, 9}
    for pi := 1; pi <= 5; pi++ {
        drawMark(colToPixel(pawnCols[pi]), rowToPixel(4))
        drawMark(colToPixel(pawnCols[pi]), rowToPixel(7))
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
            // Skip the piece being animated (it's drawn separately)
            if animActive && c == animPieceCol && r == animPieceRow {
                continue
            }
            piece := getPiece(c, r)
            if piece != nil {
                px := colToPixel(c)
                py := rowToPixel(r)

                if selectedPiece != nil && selectedPiece.col == c && selectedPiece.row == r {
                    rl.drawCircle(px, py, PIECE_RADIUS + 4, COLOR_SELECTED)
                }

                if piece.side == "red" {
                    rl.drawCircle(px, py, PIECE_RADIUS, COLOR_RED_BG)
                    rl.drawCircleLines(px, py, PIECE_RADIUS, COLOR_RED_BORDER)
                    rl.drawCircleLines(px, py, PIECE_RADIUS - 3, COLOR_RED_BORDER)
                } else {
                    rl.drawCircle(px, py, PIECE_RADIUS, COLOR_BLACK_BG)
                    rl.drawCircleLines(px, py, PIECE_RADIUS, COLOR_BLACK_BORDER)
                    rl.drawCircleLines(px, py, PIECE_RADIUS - 3, {r: 80, g: 80, b: 80, a: 255})
                }

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

// === ANIMATION ===
func startMoveAnim(fromCol, fromRow, toCol, toRow, ptype, pside) {
    animFromX = colToPixel(fromCol)
    animFromY = rowToPixel(fromRow)
    animToX = colToPixel(toCol)
    animToY = rowToPixel(toRow)
    animPieceType = ptype
    animPieceSide = pside
    animPieceCol = toCol
    animPieceRow = toRow
    animFrame = 0
    animActive = true
}

func easeOutCubic(t) {
    t = t - 1
    return t * t * t + 1
}

func drawAnimPiece() {
    if !animActive { return }

    t := animFrame / animDuration
    if t > 1 { t = 1 }
    e := easeOutCubic(t)

    px := animFromX + (animToX - animFromX) * e
    py := animFromY + (animToY - animFromY) * e

    // Shadow under moving piece
    rl.drawCircle(px + 4, py + 4, PIECE_RADIUS, {r: 0, g: 0, b: 0, a: 60})

    // Draw the piece
    if animPieceSide == "red" {
        rl.drawCircle(px, py, PIECE_RADIUS, COLOR_RED_BG)
        rl.drawCircleLines(px, py, PIECE_RADIUS, COLOR_RED_BORDER)
        rl.drawCircleLines(px, py, PIECE_RADIUS - 3, COLOR_RED_BORDER)
    } else {
        rl.drawCircle(px, py, PIECE_RADIUS, COLOR_BLACK_BG)
        rl.drawCircleLines(px, py, PIECE_RADIUS, COLOR_BLACK_BORDER)
        rl.drawCircleLines(px, py, PIECE_RADIUS - 3, {r: 80, g: 80, b: 80, a: 255})
    }

    // Label
    label := animPieceType
    tmpPiece := {type: animPieceType, side: animPieceSide}
    label = getPieceLabel(tmpPiece)
    if fontLoaded {
        tw, th := rl.measureTextEx(font, label, 26, 1)
        tx := px - tw / 2
        ty := py - th / 2
        textColor := COLOR_PIECE_TEXT_RED
        if animPieceSide == "black" {
            textColor = COLOR_PIECE_TEXT_BLACK
        }
        rl.drawTextEx(font, label, tx, ty, 26, 1, textColor)
    } else {
        tw := rl.measureText(label, 24)
        tx := px - tw / 2
        ty := py - 12
        textColor := COLOR_PIECE_TEXT_RED
        if animPieceSide == "black" {
            textColor = COLOR_PIECE_TEXT_BLACK
        }
        rl.drawText(label, tx, ty, 24, textColor)
    }
}

func drawValidMoveDots() {
    for i := 1; i <= #validMoves; i++ {
        mc := validMoves[i].col
        mr := validMoves[i].row
        px := colToPixel(mc)
        py := rowToPixel(mr)

        target := getPiece(mc, mr)
        if target != nil {
            rl.drawCircleLines(px, py, PIECE_RADIUS + 3, COLOR_VALID_MOVE)
            rl.drawCircleLines(px, py, PIECE_RADIUS + 4, COLOR_VALID_MOVE)
        } else {
            rl.drawCircle(px, py, 8, COLOR_VALID_MOVE)
        }
    }
}

func isPointInRect(px, py, rx, ry, rw, rh) {
    return px >= rx && px <= rx + rw && py >= ry && py <= ry + rh
}

func drawButton(x, y, w, h, label, hovered, disabled) {
    bgColor := {r: 80, g: 55, b: 30, a: 255}
    borderColor := {r: 180, g: 135, b: 70, a: 255}
    textColor := {r: 255, g: 240, b: 200, a: 255}
    if disabled {
        bgColor    = {r: 60, g: 58, b: 52, a: 200}
        borderColor = {r: 100, g: 95, b: 80, a: 200}
        textColor  = {r: 130, g: 125, b: 110, a: 180}
    } elseif hovered {
        bgColor    = {r: 130, g: 95, b: 50, a: 255}
        borderColor = {r: 220, g: 175, b: 90, a: 255}
    }
    // Shadow
    rl.drawRectangle(x + 3, y + 3, w, h, {r: 0, g: 0, b: 0, a: 70})
    // Body
    rl.drawRectangle(x, y, w, h, bgColor)
    // Border
    rl.drawRectangleLines(x, y, w, h, borderColor)
    // Label — pure Chinese, safe with Chinese-only font
    if fontLoaded {
        tw, th := rl.measureTextEx(font, label, 22, 1)
        tx := x + (w - tw) / 2
        ty := y + (h - th) / 2
        rl.drawTextEx(font, label, tx, ty, 22, 1, textColor)
    } else {
        tw := rl.measureText(label, 18)
        rl.drawText(label, x + (w - tw) / 2, y + (h - 18) / 2, 18, textColor)
    }
}

func drawButtons() {
    mx := rl.getMouseX()
    my := rl.getMouseY()

    // 重新开始 (always active)
    h1 := isPointInRect(mx, my, BTN1_X, BTN_Y, BTN1_W, BTN_H)
    drawButton(BTN1_X, BTN_Y, BTN1_W, BTN_H, "重新开始", h1, false)

    // 悔棋 (disabled when thinking or nothing to undo)
    canUndo := !aiThinking && #moveHistory >= 2
    h2 := canUndo && isPointInRect(mx, my, BTN2_X, BTN_Y, BTN2_W, BTN_H)
    drawButton(BTN2_X, BTN_Y, BTN2_W, BTN_H, "悔棋", h2, !canUndo)
}

func drawStatus() {
    rl.drawRectangle(0, 0, WIN_W, 55, COLOR_STATUS_BG)

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
    } elseif aiThinking {
        if fontLoaded {
            statusText = "电脑思考中"
        } else {
            statusText = "Thinking..."
        }
        statusColor = {r: 255, g: 200, b: 50, a: 255}
    } elseif gameStatus == "check" {
        if fontLoaded {
            if turn == "red" {
                statusText = "将军！红方应对"
            } else {
                statusText = "将军！电脑应对"
            }
        } else {
            statusText = "CHECK!"
        }
        statusColor = {r: 255, g: 200, b: 50, a: 255}
    } else {
        if fontLoaded {
            statusText = "红方走"
        } else {
            statusText = "RED's turn"
        }
    }

    if fontLoaded {
        rl.drawTextEx(font, statusText, 15, 8, 28, 2, statusColor)
    } else {
        rl.drawText(statusText, 15, 12, 24, statusColor)
    }

    // AI depth info — use default font (contains digits, no mixed-font issue)
    if lastAIDepth > 0 {
        rl.drawText("Depth:" .. tostring(lastAIDepth), 580, 15, 18, {r: 150, g: 150, b: 200, a: 255})
    }

    // Animated thinking dots (drawn as circles — no font issues)
    if aiThinking {
        dotBaseX := 310
        for d := 0; d <= 2; d++ {
            phase := (math.floor(frameCount / 8) + d) % 3
            a := 80
            r := 5
            if phase == 0 {
                a = 255
                r = 7
            }
            rl.drawCircle(dotBaseX + d * 18, 22, r, {r: 255, g: 200, b: 60, a: a})
        }
    }
}

func pieceTypeLabel(ptype, side) {
    if side == "red" {
        if ptype == "K" { return "帅" }
        if ptype == "A" { return "仕" }
        if ptype == "E" { return "相" }
        if ptype == "H" { return "马" }
        if ptype == "R" { return "车" }
        if ptype == "C" { return "炮" }
        if ptype == "P" { return "兵" }
    } else {
        if ptype == "K" { return "将" }
        if ptype == "A" { return "士" }
        if ptype == "E" { return "象" }
        if ptype == "H" { return "马" }
        if ptype == "R" { return "车" }
        if ptype == "C" { return "炮" }
        if ptype == "P" { return "卒" }
    }
    return ptype
}

func drawSidebar() {
    // Sidebar panel
    panelX := BOARD_X + 8 * CELL_SIZE + 30
    panelY := 60
    panelW := WIN_W - panelX - 15
    panelH := WIN_H - panelY - 60
    smallR := 16
    spacing := 38

    // Panel background
    rl.drawRectangle(panelX, panelY, panelW, panelH, {r: 45, g: 28, b: 10, a: 200})
    rl.drawRectangleLines(panelX, panelY, panelW, panelH, {r: 160, g: 120, b: 60, a: 255})

    innerX := panelX + 16
    curY := panelY + 18

    // --- Section: Red captured (black pieces eaten by red) ---
    // Section header with decorative line
    if fontLoaded {
        rl.drawTextEx(font, "红方战果", innerX, curY, 22, 1, {r: 240, g: 140, b: 80, a: 255})
    } else {
        rl.drawText("Red Captured", innerX, curY, 16, {r: 240, g: 140, b: 80, a: 255})
    }
    curY = curY + 30
    // Decorative separator line
    rl.drawRectangle(innerX, curY, panelW - 32, 2, {r: 160, g: 120, b: 60, a: 120})
    curY = curY + 12

    if #capturedBlack == 0 {
        if fontLoaded {
            rl.drawTextEx(font, "暂无", innerX + 8, curY, 16, 1, {r: 120, g: 110, b: 90, a: 180})
        } else {
            rl.drawText("None", innerX + 8, curY, 14, {r: 120, g: 110, b: 90, a: 180})
        }
        curY = curY + spacing
    } else {
        cols := 5
        for i := 1; i <= #capturedBlack; i++ {
            col := (i - 1) % cols
            row := math.floor((i - 1) / cols)
            cx := innerX + smallR + 4 + col * spacing
            cy := curY + smallR + row * spacing
            lbl := pieceTypeLabel(capturedBlack[i], "black")
            rl.drawCircle(cx, cy, smallR, COLOR_BLACK_BG)
            rl.drawCircleLines(cx, cy, smallR, {r: 80, g: 80, b: 80, a: 255})
            if fontLoaded {
                tw, th := rl.measureTextEx(font, lbl, 17, 1)
                rl.drawTextEx(font, lbl, cx - tw / 2, cy - th / 2, 17, 1, {r: 200, g: 200, b: 200, a: 255})
            }
        }
        rows1 := math.floor((#capturedBlack - 1) / cols) + 1
        curY = curY + rows1 * spacing + 8
    }

    curY = curY + 16

    // --- Section: Black captured (red pieces eaten by black) ---
    if fontLoaded {
        rl.drawTextEx(font, "黑方战果", innerX, curY, 22, 1, {r: 120, g: 160, b: 230, a: 255})
    } else {
        rl.drawText("Black Captured", innerX, curY, 16, {r: 120, g: 160, b: 230, a: 255})
    }
    curY = curY + 30
    rl.drawRectangle(innerX, curY, panelW - 32, 2, {r: 160, g: 120, b: 60, a: 120})
    curY = curY + 12

    if #capturedRed == 0 {
        if fontLoaded {
            rl.drawTextEx(font, "暂无", innerX + 8, curY, 16, 1, {r: 120, g: 110, b: 90, a: 180})
        } else {
            rl.drawText("None", innerX + 8, curY, 14, {r: 120, g: 110, b: 90, a: 180})
        }
        curY = curY + spacing
    } else {
        cols := 5
        for i := 1; i <= #capturedRed; i++ {
            col := (i - 1) % cols
            row := math.floor((i - 1) / cols)
            cx := innerX + smallR + 4 + col * spacing
            cy := curY + smallR + row * spacing
            lbl := pieceTypeLabel(capturedRed[i], "red")
            rl.drawCircle(cx, cy, smallR, COLOR_RED_BG)
            rl.drawCircleLines(cx, cy, smallR, COLOR_RED_BORDER)
            if fontLoaded {
                tw, th := rl.measureTextEx(font, lbl, 17, 1)
                rl.drawTextEx(font, lbl, cx - tw / 2, cy - th / 2, 17, 1, COLOR_PIECE_TEXT_RED)
            }
        }
        rows1 := math.floor((#capturedRed - 1) / cols) + 1
        curY = curY + rows1 * spacing + 8
    }

    curY = curY + 20

    // --- Section: Move history (last few moves) ---
    if fontLoaded {
        rl.drawTextEx(font, "走棋记录", innerX, curY, 22, 1, {r: 200, g: 190, b: 160, a: 255})
    } else {
        rl.drawText("History", innerX, curY, 16, {r: 200, g: 190, b: 160, a: 255})
    }
    curY = curY + 30
    rl.drawRectangle(innerX, curY, panelW - 32, 2, {r: 160, g: 120, b: 60, a: 120})
    curY = curY + 10

    // Show last N moves that fit
    maxMoves := 8
    startIdx := 1
    if #moveHistory > maxMoves {
        startIdx = #moveHistory - maxMoves + 1
    }
    for i := startIdx; i <= #moveHistory; i++ {
        h := moveHistory[i]
        moveNum := tostring(i)
        lbl := pieceTypeLabel(h.ptype, h.pside)
        fromStr := tostring(h.fromCol) .. "," .. tostring(h.fromRow)
        toStr := tostring(h.toCol) .. "," .. tostring(h.toRow)
        capStr := ""
        if h.capturedType != nil {
            capLbl := pieceTypeLabel(h.capturedType, h.capturedSide)
            if fontLoaded {
                capStr = " 吃" .. capLbl
            } else {
                capStr = " x" .. h.capturedType
            }
        }

        textColor := {r: 180, g: 170, b: 140, a: 230}
        if h.pside == "red" {
            textColor = {r: 230, g: 140, b: 100, a: 255}
        } else {
            textColor = {r: 130, g: 170, b: 220, a: 255}
        }

        if fontLoaded {
            line := moveNum .. ". " .. lbl .. " " .. fromStr .. " " .. toStr .. capStr
            rl.drawTextEx(font, line, innerX + 4, curY, 16, 1, textColor)
        } else {
            line := moveNum .. ". " .. h.ptype .. " " .. fromStr .. "->" .. toStr .. capStr
            rl.drawText(line, innerX + 4, curY, 14, textColor)
        }
        curY = curY + 22
    }
    if #moveHistory == 0 {
        if fontLoaded {
            rl.drawTextEx(font, "暂无", innerX + 8, curY, 16, 1, {r: 120, g: 110, b: 90, a: 180})
        } else {
            rl.drawText("None", innerX + 8, curY, 14, {r: 120, g: 110, b: 90, a: 180})
        }
    }
}

// === INPUT HANDLING ===
func handleClick(mx, my) {
    // Don't handle clicks if game is over, AI is thinking, or animating
    if gameStatus == "redwin" || gameStatus == "blackwin" || gameStatus == "draw" {
        return
    }
    if aiThinking || animActive {
        return
    }
    // Only allow player (red) to click during red's turn
    if turn != "red" {
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
        isValidDest := false
        for i := 1; i <= #validMoves; i++ {
            if validMoves[i].col == col && validMoves[i].row == row {
                isValidDest = true
                break
            }
        }
        if isValidDest {
            doMove(selectedPiece, col, row)
            selectedPiece = nil
            validMoves = {}

            // Defer AI trigger until animation finishes
            if gameStatus != "redwin" && gameStatus != "blackwin" && gameStatus != "draw" {
                animPendingAI = true
            }
            return
        }
    }

    // Try to select a piece (only red pieces for player)
    clicked := getPiece(col, row)
    if clicked != nil && clicked.side == "red" {
        selectedPiece = clicked
        validMoves = getValidMovesList(clicked)
    } else {
        selectedPiece = nil
        validMoves = {}
    }
}

// === MAIN GAME ===
rl.initWindow(WIN_W, WIN_H, "中国象棋 AI")
rl.setTargetFPS(60)

// Load Chinese font
chessChars := "帅将仕士相象马车炮兵卒楚河汉界红黑方走军胜和棋悔重新开始退出思考中深度电脑执先轮到步！战果暂无记录吃0123456789., "

fontPaths := {
    "/Library/Fonts/Arial Unicode.ttf",
    "/System/Library/Fonts/STHeiti Light.ttc",
    "/System/Library/Fonts/STHeiti Medium.ttc",
    "/System/Library/Fonts/PingFang.ttc",
    "/System/Library/Fonts/Hiragino Sans GB.ttc",
    "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
    "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc"
}

fontLoaded = false
for i := 1; i <= #fontPaths; i++ {
    if fs.exists(fontPaths[i]) {
        font = rl.loadFontChars(fontPaths[i], 52, chessChars)
        if rl.isFontReady(font) {
            fontLoaded = true
            break
        }
    }
}

initBoard()

for !rl.windowShouldClose() {
    // === AI TURN (top of loop) ===
    // Resume the AI coroutine for ONE root-move's worth of work, then return
    // to render a frame. This gives animated "thinking" at ~60ms intervals
    // instead of a single 2.5s freeze.
    if aiThinking && aiCoroutine != nil {
        ok, val := coroutine.resume(aiCoroutine)
        if coroutine.status(aiCoroutine) == "dead" {
            if ok && val != nil {
                doMove(val.piece, val.col, val.row)
            }
            aiThinking = false
            aiCoroutine = nil
        }
    }

    frameCount = frameCount + 1

    // === ANIMATION UPDATE ===
    if animActive {
        animFrame = animFrame + 1
        if animFrame >= animDuration {
            animActive = false
            // After animation ends, trigger AI if pending
            if animPendingAI {
                animPendingAI = false
                aiCoroutine = coroutine.create(func() { return getAIMove() })
                aiThinking = true
            }
        }
    }

    // === INPUT ===
    if rl.isMouseButtonPressed(0) {
        mx := rl.getMouseX()
        my := rl.getMouseY()
        if isPointInRect(mx, my, BTN1_X, BTN_Y, BTN1_W, BTN_H) {
            animActive = false
            animPendingAI = false
            initBoard()
        } elseif isPointInRect(mx, my, BTN2_X, BTN_Y, BTN2_W, BTN_H) {
            if !aiThinking && !animActive && #moveHistory >= 2 {
                undoLastMove()
                undoLastMove()
            }
        } else {
            handleClick(mx, my)
        }
    }

    if rl.isKeyPressed(rl.KEY_R) {
        animActive = false
        animPendingAI = false
        initBoard()
    }

    if rl.isKeyPressed(rl.KEY_U) {
        if !aiThinking && !animActive && #moveHistory >= 2 {
            undoLastMove()
            undoLastMove()
        }
    }

    if rl.isKeyPressed(rl.KEY_ESCAPE) {
        break
    }

    // === RENDER ===
    rl.beginDrawing()
    rl.clearBackground({r: 232, g: 218, b: 195, a: 255})

    drawBoard()
    drawLastMoveHighlight()
    drawPieces()
    drawAnimPiece()
    drawValidMoveDots()
    drawStatus()
    drawButtons()
    drawSidebar()

    rl.endDrawing()
}

rl.closeWindow()
