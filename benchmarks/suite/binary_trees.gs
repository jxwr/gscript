// Benchmark: Binary Trees
// Tests: memory allocation, GC pressure, recursive tree construction/traversal
// Adapted from the Computer Language Benchmarks Game

func makeTree(depth) {
    if depth == 0 {
        return {left: nil, right: nil}
    }
    return {left: makeTree(depth - 1), right: makeTree(depth - 1)}
}

func checkTree(node) {
    if node.left == nil { return 1 }
    return 1 + checkTree(node.left) + checkTree(node.right)
}

maxDepth := 15
if maxDepth < 6 { maxDepth = 6 }

t0 := time.now()

// Stretch tree
stretchDepth := maxDepth + 1
stretchTree := makeTree(stretchDepth)
print(string.format("stretch tree of depth %d\t check: %d", stretchDepth, checkTree(stretchTree)))
stretchTree = nil

// Long-lived tree
longLivedTree := makeTree(maxDepth)

// Iterative work
depth := 4
for depth <= maxDepth {
    iterations := 1
    d := maxDepth - depth + 4
    for k := 1; k <= d; k++ {
        iterations = iterations * 2
    }
    check := 0
    for i := 1; i <= iterations; i++ {
        check = check + checkTree(makeTree(depth))
    }
    print(string.format("%d\t trees of depth %d\t check: %d", iterations, depth, check))
    depth = depth + 2
}

print(string.format("long lived tree of depth %d\t check: %d", maxDepth, checkTree(longLivedTree)))

elapsed := time.since(t0)
print(string.format("Time: %.3fs", elapsed))
