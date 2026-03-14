// ============================================================================
// GScript Chinese Chess (Xiangqi) with AI - Negamax + Alpha-Beta + Iterative Deepening
// Player = Red (bottom), AI = Black (top)
// OPTIMIZED: piece lists, king tracking, targeted isInCheck, killer moves,
//   null move pruning, late move reduction, inlined boardKey
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
lastAITime := 0.0
aiStartTime := nil
aiResult := nil
aiDone := false
boardSnapshot := {}
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

// === OPTIMIZED: Piece lists and king tracking ===
nodeCount := 0
killerMoves := {}
redPieces := {}
blackPieces := {}
redKing := nil
blackKing := nil

// === ZOBRIST HASHING ===
// Random numbers for each (pieceType, side, col, row) combination
// pieceType: K=1,A=2,E=3,H=4,R=5,C=6,P=7  side: red=0,black=1
zobristTable := {}
zobristBlackToMove := 0
currentHash := 0

func pieceIndex(ptype) {
    if ptype == "K" { return 1 }
    if ptype == "A" { return 2 }
    if ptype == "E" { return 3 }
    if ptype == "H" { return 4 }
    if ptype == "R" { return 5 }
    if ptype == "C" { return 6 }
    if ptype == "P" { return 7 }
    return 0
}

func initZobrist() {
    // Generate pseudo-random numbers using a simple LCG
    seed := 123456789
    for pidx := 1; pidx <= 7; pidx++ {
        zobristTable[pidx] = {}
        for side := 0; side <= 1; side++ {
            zobristTable[pidx][side] = {}
            for col := 1; col <= 9; col++ {
                zobristTable[pidx][side][col] = {}
                for row := 1; row <= 10; row++ {
                    seed = (seed * 1103515245 + 12345) % 2147483648
                    zobristTable[pidx][side][col][row] = seed
                }
            }
        }
    }
    seed = (seed * 1103515245 + 12345) % 2147483648
    zobristBlackToMove = seed
}

func zobristPiece(ptype, side, col, row) {
    pidx := pieceIndex(ptype)
    sidx := 0
    if side == "black" { sidx = 1 }
    return zobristTable[pidx][sidx][col][row]
}

func computeFullHash() {
    h := 0
    for i := 1; i <= #redPieces; i++ {
        p := redPieces[i]
        if p.alive {
            h = bit32.bxor(h, zobristPiece(p.type, "red", p.col, p.row))
        }
    }
    for i := 1; i <= #blackPieces; i++ {
        p := blackPieces[i]
        if p.alive {
            h = bit32.bxor(h, zobristPiece(p.type, "black", p.col, p.row))
        }
    }
    if turn == "black" {
        h = bit32.bxor(h, zobristBlackToMove)
    }
    return h
}

// === LAZY SMP CONFIGURATION ===
NUM_WORKERS := 6

func newSearchCtx() {
    ctx := {}
    ctx.board = {}
    ctx.redPieces = {}
    ctx.blackPieces = {}
    ctx.redKing = nil
    ctx.blackKing = nil

    for i := 1; i <= #redPieces; i++ {
        p := redPieces[i]
        np := {type: p.type, side: p.side, col: p.col, row: p.row, alive: p.alive}
        ctx.redPieces[i] = np
        if p.alive {
            ctx.board[np.col * 100 + np.row] = np
        }
        if np.type == "K" { ctx.redKing = np }
    }
    for i := 1; i <= #blackPieces; i++ {
        p := blackPieces[i]
        np := {type: p.type, side: p.side, col: p.col, row: p.row, alive: p.alive}
        ctx.blackPieces[i] = np
        if p.alive {
            ctx.board[np.col * 100 + np.row] = np
        }
        if np.type == "K" { ctx.blackKing = np }
    }

    ctx.currentHash = currentHash
    ctx.killerMoves = {}
    ctx.nodeCount = 0
    ctx.ttHits = 0
    return ctx
}

func cloneSearchCtx(src) {
    ctx := {}
    ctx.board = {}
    ctx.redPieces = {}
    ctx.blackPieces = {}
    ctx.redKing = nil
    ctx.blackKing = nil

    for i := 1; i <= #src.redPieces; i++ {
        p := src.redPieces[i]
        np := {type: p.type, side: p.side, col: p.col, row: p.row, alive: p.alive}
        ctx.redPieces[i] = np
        if p.alive {
            ctx.board[np.col * 100 + np.row] = np
        }
        if np.type == "K" { ctx.redKing = np }
    }
    for i := 1; i <= #src.blackPieces; i++ {
        p := src.blackPieces[i]
        np := {type: p.type, side: p.side, col: p.col, row: p.row, alive: p.alive}
        ctx.blackPieces[i] = np
        if p.alive {
            ctx.board[np.col * 100 + np.row] = np
        }
        if np.type == "K" { ctx.blackKing = np }
    }

    ctx.currentHash = src.currentHash
    ctx.killerMoves = {}
    ctx.nodeCount = 0
    ctx.ttHits = 0
    return ctx
}

// === TRANSPOSITION TABLE ===
// Flag: 0=exact, 1=lowerbound (beta cutoff), 2=upperbound (alpha not improved)
TT_EXACT := 0
TT_LOWER := 1
TT_UPPER := 2
ttable := {}
ttHits := 0

// === PIECE HELPER ===
func makePiece(ptype, side, col, row) {
    return {type: ptype, side: side, col: col, row: row, alive: true}
}

func snapshotBoard() {
    boardSnapshot = {}
    for c := 1; c <= 9; c++ {
        for r := 1; r <= 10; r++ {
            boardSnapshot[c * 100 + r] = board[c * 100 + r]
        }
    }
}

func boardKey(col, row) {
    return col * 100 + row
}

func getPiece(col, row) {
    if col < 1 || col > 9 || row < 1 || row > 10 {
        return nil
    }
    // During AI thinking, render from snapshot to avoid seeing search state
    if aiThinking {
        return boardSnapshot[col * 100 + row]
    }
    return board[col * 100 + row]
}

func setPiece(col, row, piece) {
    if piece != nil {
        piece.col = col
        piece.row = row
    }
    board[col * 100 + row] = piece
}

func removePiece(col, row) {
    board[col * 100 + row] = nil
}

func addPiece(ptype, side, col, row) {
    p := makePiece(ptype, side, col, row)
    board[col * 100 + row] = p
    if side == "red" {
        table.insert(redPieces, p)
        if ptype == "K" { redKing = p }
    } else {
        table.insert(blackPieces, p)
        if ptype == "K" { blackKing = p }
    }
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
    aiResult = nil
    aiDone = false
    aiThinking = false
    lastAIDepth = 0
    lastAITime = 0.0
    aiStartTime = nil
    killerMoves = {}
    nodeCount = 0
    redPieces = {}
    blackPieces = {}
    redKing = nil
    blackKing = nil

    // Initialize Zobrist hashing (only once)
    if #zobristTable == 0 { initZobrist() }

    // Red pieces (bottom, rows 1-5)
    addPiece("K", "red", 5, 1)
    addPiece("A", "red", 4, 1)
    addPiece("A", "red", 6, 1)
    addPiece("E", "red", 3, 1)
    addPiece("E", "red", 7, 1)
    addPiece("H", "red", 2, 1)
    addPiece("H", "red", 8, 1)
    addPiece("R", "red", 1, 1)
    addPiece("R", "red", 9, 1)
    addPiece("C", "red", 2, 3)
    addPiece("C", "red", 8, 3)
    addPiece("P", "red", 1, 4)
    addPiece("P", "red", 3, 4)
    addPiece("P", "red", 5, 4)
    addPiece("P", "red", 7, 4)
    addPiece("P", "red", 9, 4)

    // Black pieces (top, rows 6-10)
    addPiece("K", "black", 5, 10)
    addPiece("A", "black", 4, 10)
    addPiece("A", "black", 6, 10)
    addPiece("E", "black", 3, 10)
    addPiece("E", "black", 7, 10)
    addPiece("H", "black", 2, 10)
    addPiece("H", "black", 8, 10)
    addPiece("R", "black", 1, 10)
    addPiece("R", "black", 9, 10)
    addPiece("C", "black", 2, 8)
    addPiece("C", "black", 8, 8)
    addPiece("P", "black", 1, 7)
    addPiece("P", "black", 3, 7)
    addPiece("P", "black", 5, 7)
    addPiece("P", "black", 7, 7)
    addPiece("P", "black", 9, 7)

    // Compute initial Zobrist hash
    currentHash = computeFullHash()
    ttable = {}
    ttHits = 0
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

// === FIND GENERAL POSITION (O(1) via king tracking) ===
func findGeneral(side, ctx) {
    if ctx != nil {
        k := ctx.redKing
        if side == "black" { k = ctx.blackKing }
        if k != nil && k.alive {
            return k.col, k.row
        }
        return nil, nil
    }
    k := redKing
    if side == "black" { k = blackKing }
    if k != nil && k.alive {
        return k.col, k.row
    }
    return nil, nil
}

// === TARGETED ATTACK DETECTION ===
func isSquareAttacked(gc, gr, attackerSide, ctx) {
    b := board
    if ctx != nil { b = ctx.board }

    // 1. Rook and Cannon along 4 directions
    // Up
    nr := gr + 1
    foundScreen := false
    for nr <= 10 {
        p := b[gc * 100 + nr]
        if p != nil {
            if !foundScreen {
                if p.side == attackerSide && p.type == "R" { return true }
                foundScreen = true
            } else {
                if p.side == attackerSide && p.type == "C" { return true }
                break
            }
        }
        nr = nr + 1
    }
    // Down
    nr = gr - 1
    foundScreen = false
    for nr >= 1 {
        p := b[gc * 100 + nr]
        if p != nil {
            if !foundScreen {
                if p.side == attackerSide && p.type == "R" { return true }
                foundScreen = true
            } else {
                if p.side == attackerSide && p.type == "C" { return true }
                break
            }
        }
        nr = nr - 1
    }
    // Left
    nc := gc - 1
    foundScreen = false
    for nc >= 1 {
        p := b[nc * 100 + gr]
        if p != nil {
            if !foundScreen {
                if p.side == attackerSide && p.type == "R" { return true }
                foundScreen = true
            } else {
                if p.side == attackerSide && p.type == "C" { return true }
                break
            }
        }
        nc = nc - 1
    }
    // Right
    nc = gc + 1
    foundScreen = false
    for nc <= 9 {
        p := b[nc * 100 + gr]
        if p != nil {
            if !foundScreen {
                if p.side == attackerSide && p.type == "R" { return true }
                foundScreen = true
            } else {
                if p.side == attackerSide && p.type == "C" { return true }
                break
            }
        }
        nc = nc + 1
    }

    // 2. Horse attacks (grouped by 4 blocking positions)
    bc := gc - 1
    br := gr - 1
    if bc >= 1 && br >= 1 && b[bc * 100 + br] == nil {
        if gr - 2 >= 1 {
            p := b[(gc - 1) * 100 + (gr - 2)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
        if gc - 2 >= 1 {
            p := b[(gc - 2) * 100 + (gr - 1)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
    }
    bc = gc - 1
    br = gr + 1
    if bc >= 1 && br <= 10 && b[bc * 100 + br] == nil {
        if gr + 2 <= 10 {
            p := b[(gc - 1) * 100 + (gr + 2)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
        if gc - 2 >= 1 {
            p := b[(gc - 2) * 100 + (gr + 1)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
    }
    bc = gc + 1
    br = gr - 1
    if bc <= 9 && br >= 1 && b[bc * 100 + br] == nil {
        if gr - 2 >= 1 {
            p := b[(gc + 1) * 100 + (gr - 2)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
        if gc + 2 <= 9 {
            p := b[(gc + 2) * 100 + (gr - 1)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
    }
    bc = gc + 1
    br = gr + 1
    if bc <= 9 && br <= 10 && b[bc * 100 + br] == nil {
        if gr + 2 <= 10 {
            p := b[(gc + 1) * 100 + (gr + 2)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
        if gc + 2 <= 9 {
            p := b[(gc + 2) * 100 + (gr + 1)]
            if p != nil && p.side == attackerSide && p.type == "H" { return true }
        }
    }

    // 3. Pawn attacks
    if attackerSide == "black" {
        if gr + 1 <= 10 {
            p := b[gc * 100 + (gr + 1)]
            if p != nil && p.side == "black" && p.type == "P" { return true }
        }
        if gc - 1 >= 1 {
            p := b[(gc - 1) * 100 + gr]
            if p != nil && p.side == "black" && p.type == "P" { return true }
        }
        if gc + 1 <= 9 {
            p := b[(gc + 1) * 100 + gr]
            if p != nil && p.side == "black" && p.type == "P" { return true }
        }
    } else {
        if gr - 1 >= 1 {
            p := b[gc * 100 + (gr - 1)]
            if p != nil && p.side == "red" && p.type == "P" { return true }
        }
        if gc - 1 >= 1 {
            p := b[(gc - 1) * 100 + gr]
            if p != nil && p.side == "red" && p.type == "P" { return true }
        }
        if gc + 1 <= 9 {
            p := b[(gc + 1) * 100 + gr]
            if p != nil && p.side == "red" && p.type == "P" { return true }
        }
    }

    // 4. Flying General
    ek := redKing
    if ctx != nil { ek = ctx.redKing }
    if attackerSide == "red" {
        // ek is already redKing
    } else {
        ek = blackKing
        if ctx != nil { ek = ctx.blackKing }
    }
    if ek != nil && ek.alive && ek.col == gc {
        blocked := false
        minR := gr
        maxR := ek.row
        if minR > maxR { minR = ek.row; maxR = gr }
        for checkR := minR + 1; checkR < maxR; checkR++ {
            if b[gc * 100 + checkR] != nil { blocked = true; break }
        }
        if !blocked { return true }
    }

    return false
}

// === CHECK DETECTION (using targeted attack detection) ===
func isInCheck(side, ctx) {
    gc, gr := findGeneral(side, ctx)
    if gc == nil { return true }
    enemySide := "black"
    if side == "black" { enemySide = "red" }
    return isSquareAttacked(gc, gr, enemySide, ctx)
}

// === RAW MOVE GENERATION (inlined boardKey, king tracking for flying general) ===
func getRawMoves(piece, ctx) {
    b := board
    if ctx != nil { b = ctx.board }

    moves := {}
    c := piece.col
    r := piece.row
    side := piece.side
    ptype := piece.type

    if ptype == "K" {
        nr := r + 1
        if side == "red" && nr <= 3 && c >= 4 && c <= 6 {
            p := b[c * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: c, row: nr}) }
        }
        if side == "black" && nr <= 10 && nr >= 8 && c >= 4 && c <= 6 {
            p := b[c * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: c, row: nr}) }
        }
        nr = r - 1
        if side == "red" && nr >= 1 && c >= 4 && c <= 6 {
            p := b[c * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: c, row: nr}) }
        }
        if side == "black" && nr >= 8 && c >= 4 && c <= 6 {
            p := b[c * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: c, row: nr}) }
        }
        nc := c - 1
        if nc >= 4 {
            if (side == "red" && r >= 1 && r <= 3) || (side == "black" && r >= 8 && r <= 10) {
                p := b[nc * 100 + r]
                if p == nil || p.side != side { table.insert(moves, {col: nc, row: r}) }
            }
        }
        nc = c + 1
        if nc <= 6 {
            if (side == "red" && r >= 1 && r <= 3) || (side == "black" && r >= 8 && r <= 10) {
                p := b[nc * 100 + r]
                if p == nil || p.side != side { table.insert(moves, {col: nc, row: r}) }
            }
        }
        // Flying general
        ek := blackKing
        if ctx != nil { ek = ctx.blackKing }
        if side == "black" {
            ek = redKing
            if ctx != nil { ek = ctx.redKing }
        }
        if ek != nil && ek.alive && ek.col == c {
            blocked := false
            minR := r
            maxR := ek.row
            if minR > maxR { minR = ek.row; maxR = r }
            for checkR := minR + 1; checkR < maxR; checkR++ {
                if b[c * 100 + checkR] != nil { blocked = true; break }
            }
            if !blocked { table.insert(moves, {col: ek.col, row: ek.row}) }
        }
    }

    if ptype == "A" {
        nc := c + 1; nr := r + 1
        if nc <= 6 && ((side == "red" && nr <= 3) || (side == "black" && nr <= 10 && nr >= 8)) {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c + 1; nr = r - 1
        if nc <= 6 && ((side == "red" && nr >= 1) || (side == "black" && nr >= 8)) {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c - 1; nr = r + 1
        if nc >= 4 && ((side == "red" && nr <= 3) || (side == "black" && nr <= 10 && nr >= 8)) {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c - 1; nr = r - 1
        if nc >= 4 && ((side == "red" && nr >= 1) || (side == "black" && nr >= 8)) {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
    }

    if ptype == "E" {
        nc := c + 2; nr := r + 2
        if nc <= 9 && nr <= 10 && ((side == "red" && nr <= 5) || (side == "black" && nr >= 6)) && b[(c+1)*100+(r+1)] == nil {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c + 2; nr = r - 2
        if nc <= 9 && nr >= 1 && ((side == "red" && nr <= 5) || (side == "black" && nr >= 6)) && b[(c+1)*100+(r-1)] == nil {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c - 2; nr = r + 2
        if nc >= 1 && nr <= 10 && ((side == "red" && nr <= 5) || (side == "black" && nr >= 6)) && b[(c-1)*100+(r+1)] == nil {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c - 2; nr = r - 2
        if nc >= 1 && nr >= 1 && ((side == "red" && nr <= 5) || (side == "black" && nr >= 6)) && b[(c-1)*100+(r-1)] == nil {
            p := b[nc * 100 + nr]
            if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
    }

    if ptype == "H" {
        nc := c+1; nr := r+2
        if nc <= 9 && nr <= 10 && b[c*100+(r+1)] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c+1; nr = r-2
        if nc <= 9 && nr >= 1 && b[c*100+(r-1)] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c-1; nr = r+2
        if nc >= 1 && nr <= 10 && b[c*100+(r+1)] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c-1; nr = r-2
        if nc >= 1 && nr >= 1 && b[c*100+(r-1)] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c+2; nr = r+1
        if nc <= 9 && nr <= 10 && b[(c+1)*100+r] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c+2; nr = r-1
        if nc <= 9 && nr >= 1 && b[(c+1)*100+r] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c-2; nr = r+1
        if nc >= 1 && nr <= 10 && b[(c-1)*100+r] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
        nc = c-2; nr = r-1
        if nc >= 1 && nr >= 1 && b[(c-1)*100+r] == nil {
            p := b[nc*100+nr]; if p == nil || p.side != side { table.insert(moves, {col: nc, row: nr}) }
        }
    }

    if ptype == "R" {
        nr := r + 1
        for nr <= 10 {
            p := b[c*100+nr]
            if p == nil { table.insert(moves, {col: c, row: nr}) }
            else { if p.side != side { table.insert(moves, {col: c, row: nr}) }; break }
            nr = nr + 1
        }
        nr = r - 1
        for nr >= 1 {
            p := b[c*100+nr]
            if p == nil { table.insert(moves, {col: c, row: nr}) }
            else { if p.side != side { table.insert(moves, {col: c, row: nr}) }; break }
            nr = nr - 1
        }
        nc := c + 1
        for nc <= 9 {
            p := b[nc*100+r]
            if p == nil { table.insert(moves, {col: nc, row: r}) }
            else { if p.side != side { table.insert(moves, {col: nc, row: r}) }; break }
            nc = nc + 1
        }
        nc = c - 1
        for nc >= 1 {
            p := b[nc*100+r]
            if p == nil { table.insert(moves, {col: nc, row: r}) }
            else { if p.side != side { table.insert(moves, {col: nc, row: r}) }; break }
            nc = nc - 1
        }
    }

    if ptype == "C" {
        nr := r + 1
        for nr <= 10 {
            p := b[c*100+nr]
            if p == nil { table.insert(moves, {col: c, row: nr}) }
            else {
                nr = nr + 1
                for nr <= 10 {
                    p2 := b[c*100+nr]
                    if p2 != nil { if p2.side != side { table.insert(moves, {col: c, row: nr}) }; break }
                    nr = nr + 1
                }
                break
            }
            nr = nr + 1
        }
        nr = r - 1
        for nr >= 1 {
            p := b[c*100+nr]
            if p == nil { table.insert(moves, {col: c, row: nr}) }
            else {
                nr = nr - 1
                for nr >= 1 {
                    p2 := b[c*100+nr]
                    if p2 != nil { if p2.side != side { table.insert(moves, {col: c, row: nr}) }; break }
                    nr = nr - 1
                }
                break
            }
            nr = nr - 1
        }
        nc := c + 1
        for nc <= 9 {
            p := b[nc*100+r]
            if p == nil { table.insert(moves, {col: nc, row: r}) }
            else {
                nc = nc + 1
                for nc <= 9 {
                    p2 := b[nc*100+r]
                    if p2 != nil { if p2.side != side { table.insert(moves, {col: nc, row: r}) }; break }
                    nc = nc + 1
                }
                break
            }
            nc = nc + 1
        }
        nc = c - 1
        for nc >= 1 {
            p := b[nc*100+r]
            if p == nil { table.insert(moves, {col: nc, row: r}) }
            else {
                nc = nc - 1
                for nc >= 1 {
                    p2 := b[nc*100+r]
                    if p2 != nil { if p2.side != side { table.insert(moves, {col: nc, row: r}) }; break }
                    nc = nc - 1
                }
                break
            }
            nc = nc - 1
        }
    }

    if ptype == "P" {
        if side == "red" {
            if r + 1 <= 10 {
                p := b[c*100+(r+1)]
                if p == nil || p.side != side { table.insert(moves, {col: c, row: r+1}) }
            }
            if r >= 6 {
                if c-1 >= 1 { p := b[(c-1)*100+r]; if p == nil || p.side != side { table.insert(moves, {col: c-1, row: r}) } }
                if c+1 <= 9 { p := b[(c+1)*100+r]; if p == nil || p.side != side { table.insert(moves, {col: c+1, row: r}) } }
            }
        } else {
            if r - 1 >= 1 {
                p := b[c*100+(r-1)]
                if p == nil || p.side != side { table.insert(moves, {col: c, row: r-1}) }
            }
            if r <= 5 {
                if c-1 >= 1 { p := b[(c-1)*100+r]; if p == nil || p.side != side { table.insert(moves, {col: c-1, row: r}) } }
                if c+1 <= 9 { p := b[(c+1)*100+r]; if p == nil || p.side != side { table.insert(moves, {col: c+1, row: r}) } }
            }
        }
    }

    return moves
}

// === LEGAL MOVE FILTERING (with alive flag handling) ===
func getValidMovesList(piece, ctx) {
    b := board
    if ctx != nil { b = ctx.board }

    rawMoves := getRawMoves(piece, ctx)
    legalMoves := {}
    fc := piece.col
    fr := piece.row
    side := piece.side

    for i := 1; i <= #rawMoves; i++ {
        tc := rawMoves[i].col
        tr := rawMoves[i].row
        captured := b[tc*100+tr]
        b[tc*100+tr] = piece
        b[fc*100+fr] = nil
        origCol := piece.col
        origRow := piece.row
        piece.col = tc
        piece.row = tr
        if captured != nil { captured.alive = false }

        inCheck := isInCheck(side, ctx)

        piece.col = origCol
        piece.row = origRow
        b[fc*100+fr] = piece
        if captured != nil {
            b[tc*100+tr] = captured
            captured.alive = true
        } else {
            b[tc*100+tr] = nil
        }

        if !inCheck {
            table.insert(legalMoves, {col: tc, row: tr})
        }
    }
    return legalMoves
}

// === CHECK IF ANY LEGAL MOVE EXISTS (using piece lists) ===
func hasAnyLegalMove(side) {
    pieces := redPieces
    if side == "black" { pieces = blackPieces }
    for i := 1; i <= #pieces; i++ {
        p := pieces[i]
        if p.alive {
            lm := getValidMovesList(p)
            if #lm > 0 {
                return true
            }
        }
    }
    return false
}

// === MOVE EXECUTION (with piece alive flags and king tracking) ===
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
        capturedPiece: nil,
        capturedType: nil,
        capturedSide: nil
    }
    if captured != nil {
        histEntry.capturedPiece = captured
        histEntry.capturedType = captured.type
        histEntry.capturedSide = captured.side
    }
    table.insert(moveHistory, histEntry)

    if captured != nil {
        captured.alive = false
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

// === UNDO MOVE (with piece alive flags and king tracking) ===
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

    if hist.capturedPiece != nil {
        // Restore the original captured piece object (preserving piece list references)
        restored := hist.capturedPiece
        restored.alive = true
        restored.col = hist.toCol
        restored.row = hist.toRow
        board[hist.toCol * 100 + hist.toRow] = restored

        if hist.capturedSide == "red" {
            if #capturedRed > 0 {
                table.remove(capturedRed, #capturedRed)
            }
        } else {
            if #capturedBlack > 0 {
                table.remove(capturedBlack, #capturedBlack)
            }
        }
    } elseif hist.capturedType != nil {
        // Fallback for old-style history entries without capturedPiece
        restored := makePiece(hist.capturedType, hist.capturedSide, hist.toCol, hist.toRow)
        setPiece(hist.toCol, hist.toRow, restored)
        // Re-add to piece lists
        if hist.capturedSide == "red" {
            table.insert(redPieces, restored)
            if hist.capturedType == "K" { redKing = restored }
            if #capturedRed > 0 {
                table.remove(capturedRed, #capturedRed)
            }
        } else {
            table.insert(blackPieces, restored)
            if hist.capturedType == "K" { blackKing = restored }
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
// === AI ENGINE (OPTIMIZED) ===
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

// === EVALUATION (using piece lists instead of board scan, inline PST) ===
func evaluateBoard(ctx) {
    rk := redKing
    bk := blackKing
    rp := redPieces
    bp := blackPieces
    if ctx != nil {
        rk = ctx.redKing
        bk = ctx.blackKing
        rp = ctx.redPieces
        bp = ctx.blackPieces
    }
    if !rk.alive { return -99999 }
    if !bk.alive { return 99999 }

    score := 0

    for i := 1; i <= #rp; i++ {
        p := rp[i]
        if p.alive {
            pv := pieceValue(p.type)
            c := p.col
            r := p.row
            pr := r
            pst := 0
            ptype := p.type

            if ptype == "R" {
                if c == 5 { pst = 4 } elseif c == 4 || c == 6 { pst = 2 }
                if pr >= 6 { pst = pst + 2 }
            } elseif ptype == "H" {
                if (c == 1 || c == 9) && (pr == 1 || pr == 10) { pst = -10 }
                elseif c == 1 || c == 9 || pr == 1 || pr == 10 { pst = -4 }
                elseif c >= 4 && c <= 6 && pr >= 4 && pr <= 7 { pst = 16 }
                elseif c >= 3 && c <= 7 && pr >= 3 && pr <= 8 { pst = 8 }
            } elseif ptype == "C" {
                if pr == 1 && (c == 2 || c == 8) { pst = 4 }
                if c == 5 { pst = pst + 6 }
                if pr >= 6 { pst = pst + 8 }
            } elseif ptype == "P" {
                if r >= 6 { pv = 200 }
                if pr > 5 { pst = 50 + (pr-5)*10; if c >= 4 && c <= 6 { pst = pst + 10 } }
            } elseif ptype == "A" {
                if c == 5 && pr == 2 { pst = 4 }
            } elseif ptype == "E" {
                if pr == 3 && (c == 1 || c == 5 || c == 9) { pst = 4 }
                elseif pr == 5 && (c == 3 || c == 7) { pst = 2 }
            } elseif ptype == "K" {
                if c == 5 && pr == 2 { pst = 4 }
            }
            score = score + pv + pst
        }
    }

    for i := 1; i <= #bp; i++ {
        p := bp[i]
        if p.alive {
            pv := pieceValue(p.type)
            c := p.col
            r := p.row
            pr := 11 - r
            pst := 0
            ptype := p.type

            if ptype == "R" {
                if c == 5 { pst = 4 } elseif c == 4 || c == 6 { pst = 2 }
                if pr >= 6 { pst = pst + 2 }
            } elseif ptype == "H" {
                if (c == 1 || c == 9) && (pr == 1 || pr == 10) { pst = -10 }
                elseif c == 1 || c == 9 || pr == 1 || pr == 10 { pst = -4 }
                elseif c >= 4 && c <= 6 && pr >= 4 && pr <= 7 { pst = 16 }
                elseif c >= 3 && c <= 7 && pr >= 3 && pr <= 8 { pst = 8 }
            } elseif ptype == "C" {
                if pr == 1 && (c == 2 || c == 8) { pst = 4 }
                if c == 5 { pst = pst + 6 }
                if pr >= 6 { pst = pst + 8 }
            } elseif ptype == "P" {
                if r <= 5 { pv = 200 }
                if pr > 5 { pst = 50 + (pr-5)*10; if c >= 4 && c <= 6 { pst = pst + 10 } }
            } elseif ptype == "A" {
                if c == 5 && pr == 2 { pst = 4 }
            } elseif ptype == "E" {
                if pr == 3 && (c == 1 || c == 5 || c == 9) { pst = 4 }
                elseif pr == 5 && (c == 3 || c == 7) { pst = 2 }
            } elseif ptype == "K" {
                if c == 5 && pr == 2 { pst = 4 }
            }
            score = score - pv - pst
        }
    }

    return score
}

// === GET ALL MOVES FOR A SIDE (using piece lists) ===
func getAllMovesForSide(side, ctx) {
    pieces := redPieces
    if side == "black" { pieces = blackPieces }
    if ctx != nil {
        pieces = ctx.redPieces
        if side == "black" { pieces = ctx.blackPieces }
    }
    allMoves := {}
    for i := 1; i <= #pieces; i++ {
        p := pieces[i]
        if p.alive {
            legalMoves := getValidMovesList(p, ctx)
            for j := 1; j <= #legalMoves; j++ {
                table.insert(allMoves, {piece: p, col: legalMoves[j].col, row: legalMoves[j].row})
            }
        }
    }
    return allMoves
}

// === MOVE ORDERING (captures > killers > non-captures) ===
func encodeMove(fc, fr, tc, tr) {
    return fc * 1000000 + fr * 10000 + tc * 100 + tr
}

func orderMoves(moveList, depth, ctx) {
    b := board
    km_table := killerMoves
    if ctx != nil {
        b = ctx.board
        km_table = ctx.killerMoves
    }

    captures := {}
    killers := {}
    nonCaptures := {}
    km := km_table[depth]
    km1 := 0
    km2 := 0
    if km != nil { km1 = km[1]; km2 = km[2] }

    for i := 1; i <= #moveList; i++ {
        m := moveList[i]
        target := b[m.col*100+m.row]
        if target != nil && target.side != m.piece.side {
            table.insert(captures, {piece: m.piece, col: m.col, row: m.row, captVal: pieceValue(target.type)})
        } else {
            enc := encodeMove(m.piece.col, m.piece.row, m.col, m.row)
            if enc == km1 || enc == km2 {
                table.insert(killers, {piece: m.piece, col: m.col, row: m.row, captVal: 0})
            } else {
                table.insert(nonCaptures, {piece: m.piece, col: m.col, row: m.row, captVal: 0})
            }
        }
    }
    for i := 2; i <= #captures; i++ {
        j := i
        for j > 1 && captures[j].captVal > captures[j-1].captVal {
            tmp := captures[j]; captures[j] = captures[j-1]; captures[j-1] = tmp; j = j - 1
        }
    }
    ordered := {}
    for i := 1; i <= #captures; i++ { table.insert(ordered, captures[i]) }
    for i := 1; i <= #killers; i++ { table.insert(ordered, killers[i]) }
    for i := 1; i <= #nonCaptures; i++ { table.insert(ordered, nonCaptures[i]) }
    return ordered
}

func storeKiller(depth, fc, fr, tc, tr, ctx) {
    km_table := killerMoves
    if ctx != nil { km_table = ctx.killerMoves }
    enc := encodeMove(fc, fr, tc, tr)
    km := km_table[depth]
    if km == nil { km_table[depth] = {enc, 0} }
    else { if km[1] != enc { km[2] = km[1]; km[1] = enc } }
}

// === QUIESCENCE SEARCH (search captures until position is quiet) ===
// Delta pruning: skip captures where standPat + captured_value + margin < alpha
QUIESCE_MARGIN := 200

func quiesce(alpha, beta, side, qdepth, ctx) {
    // Check stop flag for early termination
    if ctx != nil && ctx.searchStop != nil && ctx.searchStop.stopped {
        return 0
    }

    if ctx != nil {
        ctx.nodeCount = ctx.nodeCount + 1
    } else {
        nodeCount = nodeCount + 1
    }

    standPat := evaluateBoard(ctx)
    if side == "black" { standPat = -standPat }

    if standPat >= beta { return beta }
    if standPat > alpha { alpha = standPat }

    // Limit quiescence depth to avoid explosion
    if qdepth <= 0 { return alpha }

    b := board
    if ctx != nil { b = ctx.board }

    // Generate and search only capture moves
    pieces := redPieces
    if side == "black" { pieces = blackPieces }
    if ctx != nil {
        pieces = ctx.redPieces
        if side == "black" { pieces = ctx.blackPieces }
    }
    enemySide := "black"
    if side == "black" { enemySide = "red" }

    for i := 1; i <= #pieces; i++ {
        p := pieces[i]
        if !p.alive { continue }

        rawMoves := getRawMoves(p, ctx)
        for j := 1; j <= #rawMoves; j++ {
            tc := rawMoves[j].col
            tr := rawMoves[j].row
            captured := b[tc*100+tr]
            if captured == nil || captured.side == side { continue }

            // Delta pruning: skip if capturing this piece can't possibly improve alpha
            captVal := pieceValue(captured.type)
            if standPat + captVal + QUIESCE_MARGIN < alpha { continue }

            fc := p.col
            fr := p.row
            origCol := p.col
            origRow := p.row

            if ctx != nil {
                h := ctx.currentHash
                ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(p.type, side, fc, fr))
                ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(p.type, side, tc, tr))
                ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(captured.type, captured.side, tc, tr))
                ctx.currentHash = bit32.bxor(ctx.currentHash, zobristBlackToMove)

                b[fc*100+fr] = nil
                b[tc*100+tr] = p
                p.col = tc
                p.row = tr
                captured.alive = false

                legal := !isInCheck(side, ctx)
                score := 0
                if legal {
                    score = -quiesce(-beta, -alpha, enemySide, qdepth - 1, ctx)
                }

                b[tc*100+tr] = nil
                p.col = origCol
                p.row = origRow
                b[fc*100+fr] = p
                b[tc*100+tr] = captured
                captured.col = tc
                captured.row = tr
                captured.alive = true
                ctx.currentHash = h

                if !legal { continue }
                if score >= beta { return beta }
                if score > alpha { alpha = score }
            } else {
                h := currentHash
                currentHash = bit32.bxor(currentHash, zobristPiece(p.type, side, fc, fr))
                currentHash = bit32.bxor(currentHash, zobristPiece(p.type, side, tc, tr))
                currentHash = bit32.bxor(currentHash, zobristPiece(captured.type, captured.side, tc, tr))
                currentHash = bit32.bxor(currentHash, zobristBlackToMove)

                b[fc*100+fr] = nil
                b[tc*100+tr] = p
                p.col = tc
                p.row = tr
                captured.alive = false

                legal := !isInCheck(side)
                score := 0
                if legal {
                    score = -quiesce(-beta, -alpha, enemySide, qdepth - 1)
                }

                b[tc*100+tr] = nil
                p.col = origCol
                p.row = origRow
                b[fc*100+fr] = p
                b[tc*100+tr] = captured
                captured.col = tc
                captured.row = tr
                captured.alive = true
                currentHash = h

                if !legal { continue }
                if score >= beta { return beta }
                if score > alpha { alpha = score }
            }
        }
    }

    return alpha
}

// === NEGAMAX + ALPHA-BETA + TT + NULL MOVE + KILLER + LMR + QUIESCENCE ===
func negamax(depth, alpha, beta, side, allowNull, ctx) {
    // Check stop flag for early termination
    if ctx != nil && ctx.searchStop != nil && ctx.searchStop.stopped {
        return 0
    }

    if ctx != nil {
        ctx.nodeCount = ctx.nodeCount + 1
    } else {
        nodeCount = nodeCount + 1
    }

    // === Transposition table lookup ===
    ch := currentHash
    if ctx != nil { ch = ctx.currentHash }
    ttKey := ch
    if side == "black" { ttKey = bit32.bxor(ttKey, 1) }
    entry := ttable[ttKey]
    if entry != nil && entry.depth >= depth {
        if ctx != nil {
            ctx.ttHits = ctx.ttHits + 1
        } else {
            ttHits = ttHits + 1
        }
        if entry.flag == TT_EXACT { return entry.score }
        if entry.flag == TT_LOWER && entry.score >= beta { return entry.score }
        if entry.flag == TT_UPPER && entry.score <= alpha { return entry.score }
    }

    if depth <= 0 {
        return quiesce(alpha, beta, side, 4, ctx)
    }

    inCheck := isInCheck(side, ctx)

    b := board
    if ctx != nil { b = ctx.board }

    // Null move pruning
    if allowNull && !inCheck && depth >= 3 {
        enemySide := "black"
        if side == "black" { enemySide = "red" }

        if ctx != nil {
            ctx.currentHash = bit32.bxor(ctx.currentHash, zobristBlackToMove)
            score := -negamax(depth - 3, -beta, -beta + 1, enemySide, false, ctx)
            ctx.currentHash = bit32.bxor(ctx.currentHash, zobristBlackToMove)
            if score >= beta { return beta }
        } else {
            currentHash = bit32.bxor(currentHash, zobristBlackToMove)
            score := -negamax(depth - 3, -beta, -beta + 1, enemySide, false)
            currentHash = bit32.bxor(currentHash, zobristBlackToMove)
            if score >= beta { return beta }
        }
    }

    allMoves := getAllMovesForSide(side, ctx)
    if #allMoves == 0 { return -99000 }
    allMoves = orderMoves(allMoves, depth, ctx)

    enemySide := "black"
    if side == "black" { enemySide = "red" }

    origAlpha := alpha
    bestScore := -999999

    for i := 1; i <= #allMoves; i++ {
        m := allMoves[i]
        p := m.piece
        tc := m.col
        tr := m.row
        fc := p.col
        fr := p.row

        captured := b[tc*100+tr]
        origCol := p.col
        origRow := p.row

        if ctx != nil {
            // Update hash incrementally
            h := ctx.currentHash
            ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(p.type, side, fc, fr))
            ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(p.type, side, tc, tr))
            if captured != nil {
                ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(captured.type, captured.side, tc, tr))
            }
            ctx.currentHash = bit32.bxor(ctx.currentHash, zobristBlackToMove)

            b[fc*100+fr] = nil
            b[tc*100+tr] = p
            p.col = tc
            p.row = tr
            if captured != nil { captured.alive = false }

            // Late Move Reduction
            score := 0
            if i >= 5 && depth >= 3 && captured == nil && !inCheck {
                score = -negamax(depth - 2, -alpha - 1, -alpha, enemySide, true, ctx)
                if score > alpha {
                    score = -negamax(depth - 1, -beta, -alpha, enemySide, true, ctx)
                }
            } else {
                score = -negamax(depth - 1, -beta, -alpha, enemySide, true, ctx)
            }

            // Undo
            b[tc*100+tr] = nil
            p.col = origCol
            p.row = origRow
            b[fc*100+fr] = p
            if captured != nil {
                b[tc*100+tr] = captured
                captured.col = tc
                captured.row = tr
                captured.alive = true
            }
            ctx.currentHash = h

            if score > bestScore { bestScore = score }
            if score >= beta {
                if captured == nil { storeKiller(depth, fc, fr, tc, tr, ctx) }
                ttable[ttKey] = {depth: depth, score: beta, flag: TT_LOWER}
                return beta
            }
            if score > alpha { alpha = score }
        } else {
            // Update hash incrementally
            h := currentHash
            currentHash = bit32.bxor(currentHash, zobristPiece(p.type, side, fc, fr))
            currentHash = bit32.bxor(currentHash, zobristPiece(p.type, side, tc, tr))
            if captured != nil {
                currentHash = bit32.bxor(currentHash, zobristPiece(captured.type, captured.side, tc, tr))
            }
            currentHash = bit32.bxor(currentHash, zobristBlackToMove)

            b[fc*100+fr] = nil
            b[tc*100+tr] = p
            p.col = tc
            p.row = tr
            if captured != nil { captured.alive = false }

            // Late Move Reduction
            score := 0
            if i >= 5 && depth >= 3 && captured == nil && !inCheck {
                score = -negamax(depth - 2, -alpha - 1, -alpha, enemySide, true)
                if score > alpha {
                    score = -negamax(depth - 1, -beta, -alpha, enemySide, true)
                }
            } else {
                score = -negamax(depth - 1, -beta, -alpha, enemySide, true)
            }

            // Undo
            b[tc*100+tr] = nil
            p.col = origCol
            p.row = origRow
            b[fc*100+fr] = p
            if captured != nil {
                b[tc*100+tr] = captured
                captured.col = tc
                captured.row = tr
                captured.alive = true
            }
            currentHash = h

            if score > bestScore { bestScore = score }
            if score >= beta {
                if captured == nil { storeKiller(depth, fc, fr, tc, tr) }
                ttable[ttKey] = {depth: depth, score: beta, flag: TT_LOWER}
                return beta
            }
            if score > alpha { alpha = score }
        }
    }

    // Store TT entry
    flag := TT_EXACT
    if alpha <= origAlpha { flag = TT_UPPER }
    ttable[ttKey] = {depth: depth, score: alpha, flag: flag}

    return alpha
}

// === GET AI MOVE (Lazy SMP Parallel Search) ===
func getAIMove() {
    startTime := time.now()
    killerMoves = {}
    nodeCount = 0
    ttHits = 0

    // Recompute hash from current position (in case doMove didn't track it)
    currentHash = computeFullHash()

    // Launch parallel workers
    resultCh := make(chan, NUM_WORKERS)
    searchStop := {stopped: false, maxDepth: 0}

    for w := 0; w < NUM_WORKERS; w++ {
        ctx := newSearchCtx()
        ctx.searchStop = searchStop
        startDepth := 1 + (w % 2)
        go func() {
            bestMove := nil
            bestDepth := 0
            bestScore := -999999

            for depth := startDepth; depth <= 12; depth++ {
                if searchStop.stopped { break }

                alpha := -999999
                beta := 999999
                localBest := nil
                depthComplete := true

                allMoves := getAllMovesForSide("black", ctx)
                allMoves = orderMoves(allMoves, depth + 1, ctx)
                if #allMoves == 0 { break }

                for i := 1; i <= #allMoves; i++ {
                    if searchStop.stopped { depthComplete = false; break }

                    m := allMoves[i]
                    p := m.piece
                    tc := m.col
                    tr := m.row
                    fc := p.col
                    fr := p.row

                    captured := ctx.board[tc*100+tr]
                    origCol := p.col
                    origRow := p.row

                    h := ctx.currentHash
                    ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(p.type, "black", fc, fr))
                    ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(p.type, "black", tc, tr))
                    if captured != nil {
                        ctx.currentHash = bit32.bxor(ctx.currentHash, zobristPiece(captured.type, captured.side, tc, tr))
                    }
                    ctx.currentHash = bit32.bxor(ctx.currentHash, zobristBlackToMove)

                    ctx.board[fc*100+fr] = nil
                    ctx.board[tc*100+tr] = p
                    p.col = tc
                    p.row = tr
                    if captured != nil { captured.alive = false }

                    score := -negamax(depth - 1, -beta, -alpha, "red", true, ctx)

                    ctx.board[tc*100+tr] = nil
                    p.col = origCol
                    p.row = origRow
                    ctx.board[fc*100+fr] = p
                    if captured != nil {
                        ctx.board[tc*100+tr] = captured
                        captured.col = tc
                        captured.row = tr
                        captured.alive = true
                    }
                    ctx.currentHash = h

                    if score > alpha {
                        alpha = score
                        localBest = {fromCol: fc, fromRow: fr, col: tc, row: tr, ptype: p.type, pside: p.side}
                    }
                }

                // Only use results from fully completed depth iterations
                if depthComplete && localBest != nil {
                    bestMove = localBest
                    bestDepth = depth
                    bestScore = alpha
                    // Report progress to parent
                    if depth > searchStop.maxDepth {
                        searchStop.maxDepth = depth
                    }
                }

                if alpha >= 99000 { break }
            }

            resultCh <- {move: bestMove, depth: bestDepth, score: bestScore}
        }()
    }

    // Dynamic stop: wait until (time >= 3s AND depth >= 5) OR time >= 15s
    for {
        time.sleep(0.1)
        elapsed := time.since(startTime)
        if elapsed >= 3.0 && searchStop.maxDepth >= 5 {
            break
        }
        if elapsed >= 15.0 {
            break
        }
    }
    searchStop.stopped = true

    // Collect results from all workers — prefer deepest complete search
    bestMove := nil
    bestDepth := 0
    bestScore := -999999
    for w := 0; w < NUM_WORKERS; w++ {
        result := <-resultCh
        if result.move != nil {
            // Prefer deeper search; at same depth prefer higher score
            if result.depth > bestDepth || (result.depth == bestDepth && result.score > bestScore) {
                bestDepth = result.depth
                bestMove = result.move
                bestScore = result.score
            }
        }
    }

    lastAIDepth = bestDepth
    lastAITime = time.since(startTime)

    // Convert coordinate-based result back to piece reference on the real board
    if bestMove != nil {
        p := board[bestMove.fromCol * 100 + bestMove.fromRow]
        if p != nil {
            return {piece: p, col: bestMove.col, row: bestMove.row}
        }
    }
    return nil
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

    // AI info — show live timer while thinking, final result after
    if aiThinking && aiStartTime != nil {
        elapsed := time.since(aiStartTime)
        secStr := tostring(math.floor(elapsed))
        rl.drawText(secStr .. "s", 580, 15, 18, {r: 255, g: 200, b: 60, a: 255})
    } elseif lastAIDepth > 0 {
        timeStr := tostring(math.floor(lastAITime * 10) / 10)
        rl.drawText("D" .. tostring(lastAIDepth) .. " " .. timeStr .. "s", 570, 15, 18, {r: 150, g: 150, b: 200, a: 255})
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

// === TEST MODE ===
if arg != nil && #arg >= 1 && arg[1] == "test" {
    print("=== Chess AI Test Mode ===")
    initBoard()
    print("Board initialized, pieces:", #redPieces, "red,", #blackPieces, "black")

    // Test 0: Basic board sanity check
    print("")
    print("--- Test 0: Board sanity ---")
    allBlack := getAllMovesForSide("black")
    print("Black moves available:", #allBlack)
    if #allBlack > 0 {
        m := allBlack[1]
        print("  First move:", m.piece.type, m.piece.side, "from", m.piece.col, m.piece.row, "to", m.col, m.row)
    }

    // Test 1: Direct call to getAIMove (blocking, no goroutine)
    print("")
    print("--- Test 1: Direct getAIMove call ---")
    t1 := time.now()
    result := getAIMove()
    elapsed1 := time.since(t1)
    if result != nil {
        print("PASS: getAIMove returned move:", result.piece.type, result.piece.side,
              "to col:", result.col, "row:", result.row,
              "depth:", lastAIDepth, "time:", elapsed1)
    } else {
        print("FAIL: getAIMove returned nil")
    }
    if elapsed1 >= 3.0 {
        print("PASS: took", elapsed1, "seconds (depth", lastAIDepth, ")")
    } else {
        print("WARNING: unexpected time:", elapsed1)
    }

    // Test 2: Goroutine pattern (same as game)
    print("")
    print("--- Test 2: Goroutine pattern (game flow) ---")
    initBoard()
    aiThinking = true
    aiDone = false
    aiResult = nil

    go func() {
        move := getAIMove()
        aiResult = move
        aiDone = true
    }()

    t2 := time.now()
    detected := false
    for tick := 0; tick < 1000; tick++ {
        if aiThinking && aiDone {
            elapsed2 := time.since(t2)
            print("PASS: detected aiDone at tick", tick, "time:", elapsed2)
            if aiResult != nil {
                print("  Move:", aiResult.piece.type, aiResult.piece.side,
                      "to col:", aiResult.col, "row:", aiResult.row)
            } else {
                print("  WARNING: aiResult is nil")
            }
            detected = true
            break
        }
        time.sleep(0.02)
    }
    if !detected {
        print("FAIL: goroutine never set aiDone!")
        print("  aiDone =", aiDone, "aiResult =", aiResult)
    }

    print("")
    print("=== All tests complete ===")
    return
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
    // === AI TURN (goroutine-based for non-blocking UI) ===
    if aiThinking && aiDone {
        result := aiResult
        aiThinking = false
        aiDone = false
        aiResult = nil
        if result != nil {
            doMove(result.piece, result.col, result.row)
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
                snapshotBoard()
                aiThinking = true
                aiStartTime = time.now()
                go func() {
                    move := getAIMove()
                    aiResult = move
                    aiDone = true
                }()
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
