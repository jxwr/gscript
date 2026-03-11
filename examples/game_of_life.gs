// game_of_life.gs - Conway's Game of Life in GScript
// Demonstrates: grid manipulation, neighbor counting, cellular automata rules

print("=== Conway's Game of Life ===")
print()

// -------------------------------------------------------
// Grid creation and manipulation
// -------------------------------------------------------

func newGrid(width, height) {
    grid := {}
    grid.width = width
    grid.height = height
    grid.cells = {}

    // Initialize all cells to dead (0)
    for y := 1; y <= height; y++ {
        grid.cells[y] = {}
        for x := 1; x <= width; x++ {
            grid.cells[y][x] = 0
        }
    }

    return grid
}

func setCell(grid, x, y, alive) {
    if x >= 1 && x <= grid.width && y >= 1 && y <= grid.height {
        if alive {
            grid.cells[y][x] = 1
        } else {
            grid.cells[y][x] = 0
        }
    }
}

func getCell(grid, x, y) {
    if x < 1 || x > grid.width || y < 1 || y > grid.height {
        return 0
    }
    return grid.cells[y][x]
}

// -------------------------------------------------------
// Count neighbors
// -------------------------------------------------------

func countNeighbors(grid, x, y) {
    count := 0
    for dy := -1; dy <= 1; dy++ {
        for dx := -1; dx <= 1; dx++ {
            if dx != 0 || dy != 0 {
                count = count + getCell(grid, x + dx, y + dy)
            }
        }
    }
    return count
}

// -------------------------------------------------------
// Apply rules (one generation)
// -------------------------------------------------------

func nextGeneration(grid) {
    newG := newGrid(grid.width, grid.height)

    for y := 1; y <= grid.height; y++ {
        for x := 1; x <= grid.width; x++ {
            neighbors := countNeighbors(grid, x, y)
            alive := getCell(grid, x, y) == 1

            if alive {
                // Live cell survives with 2 or 3 neighbors
                if neighbors == 2 || neighbors == 3 {
                    setCell(newG, x, y, true)
                }
            } else {
                // Dead cell becomes alive with exactly 3 neighbors
                if neighbors == 3 {
                    setCell(newG, x, y, true)
                }
            }
        }
    }

    return newG
}

// -------------------------------------------------------
// Print grid to terminal
// -------------------------------------------------------

func printGrid(grid, gen) {
    print(string.format("  Generation %d:", gen))
    border := "  +" .. string.rep("-", grid.width) .. "+"
    print(border)
    for y := 1; y <= grid.height; y++ {
        row := "  |"
        for x := 1; x <= grid.width; x++ {
            if grid.cells[y][x] == 1 {
                row = row .. "#"
            } else {
                row = row .. " "
            }
        }
        row = row .. "|"
        print(row)
    }
    print(border)
}

// Count live cells
func countLive(grid) {
    count := 0
    for y := 1; y <= grid.height; y++ {
        for x := 1; x <= grid.width; x++ {
            count = count + grid.cells[y][x]
        }
    }
    return count
}

// -------------------------------------------------------
// Pattern loading
// -------------------------------------------------------

func placePattern(grid, pattern, offsetX, offsetY) {
    for i := 1; i <= #pattern; i++ {
        coords := pattern[i]
        setCell(grid, coords[1] + offsetX, coords[2] + offsetY, true)
    }
}

// -------------------------------------------------------
// Interesting starting patterns
// -------------------------------------------------------

// 1. Glider - moves diagonally
print("--- Glider Pattern ---")

glider := {
    {2, 1}, {3, 2}, {1, 3}, {2, 3}, {3, 3}
}

grid := newGrid(15, 15)
placePattern(grid, glider, 2, 2)

for gen := 0; gen <= 8; gen++ {
    if gen % 2 == 0 {
        printGrid(grid, gen)
        print("  Live cells:", countLive(grid))
        print()
    }
    grid = nextGeneration(grid)
}

// 2. Blinker - period 2 oscillator
print("--- Blinker Pattern (period 2) ---")

blinker := {
    {2, 1}, {2, 2}, {2, 3}
}

grid2 := newGrid(7, 7)
placePattern(grid2, blinker, 2, 2)

for gen := 0; gen <= 3; gen++ {
    printGrid(grid2, gen)
    print()
    grid2 = nextGeneration(grid2)
}

// 3. Block - still life
print("--- Block Pattern (still life) ---")

block := {
    {1, 1}, {2, 1}, {1, 2}, {2, 2}
}

grid3 := newGrid(6, 6)
placePattern(grid3, block, 2, 2)

printGrid(grid3, 0)
grid3 = nextGeneration(grid3)
printGrid(grid3, 1)
print("  Block is stable:", countLive(grid3) == 4)
print()

// 4. Toad - period 2 oscillator
print("--- Toad Pattern (period 2) ---")

toad := {
    {2, 1}, {3, 1}, {4, 1},
    {1, 2}, {2, 2}, {3, 2}
}

grid4 := newGrid(8, 6)
placePattern(grid4, toad, 2, 2)

for gen := 0; gen <= 3; gen++ {
    printGrid(grid4, gen)
    print()
    grid4 = nextGeneration(grid4)
}

// 5. R-pentomino - a chaotic pattern
print("--- R-pentomino (chaotic!) ---")

rpentomino := {
    {2, 1}, {3, 1},
    {1, 2}, {2, 2},
    {2, 3}
}

grid5 := newGrid(20, 20)
placePattern(grid5, rpentomino, 9, 9)

print("  R-pentomino evolves chaotically.")
print("  Showing generations 0, 5, 10, 15, 20:")
for gen := 0; gen <= 20; gen++ {
    if gen == 0 || gen == 5 || gen == 10 || gen == 15 || gen == 20 {
        printGrid(grid5, gen)
        print("  Live cells:", countLive(grid5))
        print()
    }
    grid5 = nextGeneration(grid5)
}

// 6. Die Hard - a methuselah that dies after 130 generations
print("--- Die Hard Pattern ---")

diehard := {
    {7, 1},
    {1, 2}, {2, 2},
    {2, 3}, {6, 3}, {7, 3}, {8, 3}
}

grid6 := newGrid(20, 12)
placePattern(grid6, diehard, 5, 4)

printGrid(grid6, 0)
print("  Live cells:", countLive(grid6))
print()

// Evolve for a few generations
for gen := 1; gen <= 30; gen++ {
    grid6 = nextGeneration(grid6)
}
printGrid(grid6, 30)
print("  Live cells at gen 30:", countLive(grid6))
print()

print("=== Done ===")
