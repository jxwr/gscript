// ============================================================================
// GScript Chinese Chess (Xiangqi) Benchmark: Single-threaded vs Parallel (Lazy SMP)
// Measures depth-6 search performance with identical board positions
// ============================================================================

board := {}
nodeCount := 0
killerMoves := {}
redPieces := {}
blackPieces := {}
redKing := nil
blackKing := nil

// === ZOBRIST HASHING ===
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
    return h
}

// === TRANSPOSITION TABLE (SHARED GLOBAL for Lazy SMP) ===
TT_EXACT := 0
TT_LOWER := 1
TT_UPPER := 2
ttable := {}
ttHits := 0

// === CONFIGURATION ===
NUM_WORKERS := 6
TIME_BUDGET := 10.0
MAX_DEPTH := 12

func makePiece(ptype, side, col, row) {
    return {type: ptype, side: side, col: col, row: row, alive: true}
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

func initBoard() {
    board = {}
    redPieces = {}
    blackPieces = {}
    redKing = nil
    blackKing = nil

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
}

// === SEARCH CONTEXT (deep copy for parallel workers) ===
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

// === FIND GENERAL ===
func findGeneral(side, ctx) {
    if ctx != nil {
        k := ctx.redKing
        if side == "black" { k = ctx.blackKing }
        if k != nil && k.alive { return k.col, k.row }
        return nil, nil
    }
    k := redKing
    if side == "black" { k = blackKing }
    if k != nil && k.alive { return k.col, k.row }
    return nil, nil
}

// === TARGETED ATTACK DETECTION ===
func isSquareAttacked(gc, gr, attackerSide, ctx) {
    b := board
    if ctx != nil { b = ctx.board }

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

func isInCheck(side, ctx) {
    gc, gr := findGeneral(side, ctx)
    if gc == nil { return true }
    enemySide := "black"
    if side == "black" { enemySide = "red" }
    return isSquareAttacked(gc, gr, enemySide, ctx)
}

// === RAW MOVE GENERATION ===
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

// === LEGAL MOVE FILTERING ===
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

// === EVALUATION ===
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

// === GET ALL MOVES FOR A SIDE ===
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

// === MOVE ORDERING ===
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

// === QUIESCENCE SEARCH ===
QUIESCE_MARGIN := 200

func quiesce(alpha, beta, side, qdepth, ctx) {
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

    if qdepth <= 0 { return alpha }

    b := board
    if ctx != nil { b = ctx.board }

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

                legal := !isInCheck(side, nil)
                score := 0
                if legal {
                    score = -quiesce(-beta, -alpha, enemySide, qdepth - 1, nil)
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
    if ctx != nil && ctx.searchStop != nil && ctx.searchStop.stopped {
        return 0
    }

    if ctx != nil {
        ctx.nodeCount = ctx.nodeCount + 1
    } else {
        nodeCount = nodeCount + 1
    }

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
            score := -negamax(depth - 3, -beta, -beta + 1, enemySide, false, nil)
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

            score := 0
            if i >= 5 && depth >= 3 && captured == nil && !inCheck {
                score = -negamax(depth - 2, -alpha - 1, -alpha, enemySide, true, ctx)
                if score > alpha {
                    score = -negamax(depth - 1, -beta, -alpha, enemySide, true, ctx)
                }
            } else {
                score = -negamax(depth - 1, -beta, -alpha, enemySide, true, ctx)
            }

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

            score := 0
            if i >= 5 && depth >= 3 && captured == nil && !inCheck {
                score = -negamax(depth - 2, -alpha - 1, -alpha, enemySide, true, nil)
                if score > alpha {
                    score = -negamax(depth - 1, -beta, -alpha, enemySide, true, nil)
                }
            } else {
                score = -negamax(depth - 1, -beta, -alpha, enemySide, true, nil)
            }

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
                if captured == nil { storeKiller(depth, fc, fr, tc, tr, nil) }
                ttable[ttKey] = {depth: depth, score: beta, flag: TT_LOWER}
                return beta
            }
            if score > alpha { alpha = score }
        }
    }

    flag := TT_EXACT
    if alpha <= origAlpha { flag = TT_UPPER }
    ttable[ttKey] = {depth: depth, score: alpha, flag: flag}

    return alpha
}

func pieceLabel(ptype) {
    if ptype == "K" { return "King" }
    if ptype == "R" { return "Rook" }
    if ptype == "H" { return "Horse" }
    if ptype == "C" { return "Cannon" }
    if ptype == "E" { return "Elephant" }
    if ptype == "A" { return "Advisor" }
    if ptype == "P" { return "Pawn" }
    return ptype
}

// ============================================================================
// === BENCHMARK MAIN ===
// ============================================================================

// searchAtDepthSingle: single-threaded search at a given depth using globals
func searchAtDepthSingle(depth) {
    nodeCount = 0
    ttHits = 0

    alpha := -999999
    beta := 999999
    localBest := nil

    allMoves := getAllMovesForSide("black", nil)
    allMoves = orderMoves(allMoves, depth + 1, nil)

    for i := 1; i <= #allMoves; i++ {
        m := allMoves[i]
        p := m.piece
        tc := m.col
        tr := m.row
        fc := p.col
        fr := p.row

        captured := board[tc*100+tr]
        origCol := p.col
        origRow := p.row

        h := currentHash
        currentHash = bit32.bxor(currentHash, zobristPiece(p.type, "black", fc, fr))
        currentHash = bit32.bxor(currentHash, zobristPiece(p.type, "black", tc, tr))
        if captured != nil {
            currentHash = bit32.bxor(currentHash, zobristPiece(captured.type, captured.side, tc, tr))
        }
        currentHash = bit32.bxor(currentHash, zobristBlackToMove)

        board[fc*100+fr] = nil
        board[tc*100+tr] = p
        p.col = tc
        p.row = tr
        if captured != nil { captured.alive = false }

        score := -negamax(depth - 1, -beta, -alpha, "red", true, nil)

        board[tc*100+tr] = nil
        p.col = origCol
        p.row = origRow
        board[fc*100+fr] = p
        if captured != nil {
            board[tc*100+tr] = captured
            captured.col = tc
            captured.row = tr
            captured.alive = true
        }
        currentHash = h

        if score > alpha {
            alpha = score
            localBest = {ptype: p.type, fromCol: fc, fromRow: fr, col: tc, row: tr}
        }
    }

    return localBest, alpha, nodeCount, ttHits
}

print("==============================================================")
print("  Xiangqi AI Benchmark: Single-threaded vs Parallel (Lazy SMP)")
print(string.format("  Time budget: %.0fs   Workers: %d", TIME_BUDGET, NUM_WORKERS))
print("==============================================================")
print("")

initZobrist()

// ============================================================================
// PHASE 1: Single-threaded iterative deepening (time-budgeted)
// ============================================================================
print(string.format("--- Phase 1: Single-threaded (%.0fs budget) ---", TIME_BUDGET))
print(string.format("%-7s  %10s  %10s  %8s  %8s  %s", "Depth", "Time (s)", "Nodes", "TT Hits", "Score", "Best Move"))
print("---------------------------------------------------------------")

initBoard()
currentHash = computeFullHash()
killerMoves = {}
ttable = {}

singleBestMove := nil
singleBestScore := 0
singleMaxDepth := 0
singleTotalNodes := 0
singleTotalTTHits := 0

t0 := time.now()

for depth := 1; depth <= MAX_DEPTH; depth++ {
    localBest, alpha, nodes, hits := searchAtDepthSingle(depth)

    singleTotalNodes = singleTotalNodes + nodes
    singleTotalTTHits = singleTotalTTHits + hits
    if localBest != nil {
        singleBestMove = localBest
        singleBestScore = alpha
        singleMaxDepth = depth
    }

    elapsed := time.since(t0)
    moveStr := "none"
    if localBest != nil {
        moveStr = string.format("%s (%d,%d)->(%d,%d)",
            pieceLabel(localBest.ptype),
            localBest.fromCol, localBest.fromRow, localBest.col, localBest.row)
    }
    print(string.format("  d=%-3d  %10.3f  %10d  %8d  %8d  %s", depth, elapsed, nodes, hits, alpha, moveStr))

    if elapsed >= TIME_BUDGET { break }
}

singleTime := time.since(t0)
print("---------------------------------------------------------------")
singleMoveStr := "none"
if singleBestMove != nil {
    singleMoveStr = string.format("%s (%d,%d)->(%d,%d)",
        pieceLabel(singleBestMove.ptype),
        singleBestMove.fromCol, singleBestMove.fromRow, singleBestMove.col, singleBestMove.row)
}
print(string.format("Single: %.3fs, depth %d, %d nodes, score=%d, move=%s",
    singleTime, singleMaxDepth, singleTotalNodes, singleBestScore, singleMoveStr))
print("")

// ============================================================================
// PHASE 2: Parallel Lazy SMP (same time budget)
// ============================================================================
print(string.format("--- Phase 2: Parallel Lazy SMP (%d workers, %.0fs budget) ---", NUM_WORKERS, TIME_BUDGET))

initBoard()
currentHash = computeFullHash()
ttable = {}

resultCh := make(chan, NUM_WORKERS)
searchStop := {stopped: false, maxDepth: 0}

t0 = time.now()

for w := 0; w < NUM_WORKERS; w++ {
    ctx := newSearchCtx()
    ctx.searchStop = searchStop
    startDepth := 1 + (w % 2)
    go func() {
        bestMove := nil
        bestDepth := 0
        bestScore := -999999

        for depth := startDepth; depth <= MAX_DEPTH; depth++ {
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
                    localBest = {ptype: p.type, fromCol: fc, fromRow: fr, col: tc, row: tr}
                }
            }

            if depthComplete && localBest != nil {
                bestMove = localBest
                bestDepth = depth
                bestScore = alpha
                if depth > searchStop.maxDepth {
                    searchStop.maxDepth = depth
                }
            }

            if alpha >= 99000 { break }
        }

        resultCh <- {move: bestMove, depth: bestDepth, score: bestScore, nodes: ctx.nodeCount, ttHits: ctx.ttHits}
    }()
}

// Wait for time budget, then signal stop
for {
    time.sleep(0.1)
    elapsed := time.since(t0)
    if elapsed >= TIME_BUDGET { break }
}
searchStop.stopped = true

// Collect results from all workers
parBestMove := nil
parBestDepth := 0
parBestScore := -999999
parTotalNodes := 0
parTotalTTHits := 0

for w := 0; w < NUM_WORKERS; w++ {
    result := <-resultCh
    parTotalNodes = parTotalNodes + result.nodes
    parTotalTTHits = parTotalTTHits + result.ttHits
    if result.move != nil {
        if result.depth > parBestDepth || (result.depth == parBestDepth && result.score > parBestScore) {
            parBestDepth = result.depth
            parBestMove = result.move
            parBestScore = result.score
        }
    }
    print(string.format("  Worker %d: depth=%d, nodes=%d, TT hits=%d, score=%d", w+1, result.depth, result.nodes, result.ttHits, result.score))
}

parallelTime := time.since(t0)
print("---------------------------------------------------------------")
parMoveStr := "none"
if parBestMove != nil {
    parMoveStr = string.format("%s (%d,%d)->(%d,%d)",
        pieceLabel(parBestMove.ptype),
        parBestMove.fromCol, parBestMove.fromRow, parBestMove.col, parBestMove.row)
}
print(string.format("Parallel: %.3fs, depth %d, %d nodes, score=%d, move=%s",
    parallelTime, parBestDepth, parTotalNodes, parBestScore, parMoveStr))
print("")

// ============================================================================
// PHASE 3: Summary
// ============================================================================
print("==============================================================")
print("  RESULTS")
print("==============================================================")
print(string.format("  Single-threaded:  depth %-2d  %10d nodes  %.1fs", singleMaxDepth, singleTotalNodes, singleTime))
print(string.format("  Parallel (%dw):    depth %-2d  %10d nodes  %.1fs", NUM_WORKERS, parBestDepth, parTotalNodes, parallelTime))
print("")
depthGain := parBestDepth - singleMaxDepth
if depthGain > 0 {
    print(string.format("  Parallel searches %d levels DEEPER in the same time!", depthGain))
} elseif depthGain == 0 {
    print("  Same depth reached. Parallel overhead offsets worker count.")
} else {
    print(string.format("  Single-threaded went %d levels deeper. Parallel overhead too high.", -depthGain))
}
print(string.format("  Single best:   %s  score=%d", singleMoveStr, singleBestScore))
print(string.format("  Parallel best: %s  score=%d", parMoveStr, parBestScore))
print("==============================================================")
print("Benchmark complete.")
