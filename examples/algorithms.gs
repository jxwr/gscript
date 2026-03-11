// algorithms.gs - Classic algorithms implemented in GScript
// Demonstrates: quicksort, merge sort, binary search, BFS, DFS, Dijkstra, fibonacci

print("=== Classic Algorithms ===")
print()

// Helper to join table values as string
func joinValues(tbl) {
    parts := {}
    for i := 1; i <= #tbl; i++ {
        table.insert(parts, tostring(tbl[i]))
    }
    return table.concat(parts, ", ")
}

// Helper to copy a table
func copyTable(tbl) {
    result := {}
    for i := 1; i <= #tbl; i++ {
        table.insert(result, tbl[i])
    }
    return result
}

// -------------------------------------------------------
// 1. Quicksort
// -------------------------------------------------------
print("--- Quicksort ---")

func quicksort(arr, low, high) {
    if low >= high { return nil }

    // Partition
    pivot := arr[high]
    i := low
    for j := low; j < high; j++ {
        if arr[j] <= pivot {
            // Swap arr[i] and arr[j]
            arr[i], arr[j] = arr[j], arr[i]
            i = i + 1
        }
    }
    // Swap pivot into position
    arr[i], arr[high] = arr[high], arr[i]

    // Recurse
    quicksort(arr, low, i - 1)
    quicksort(arr, i + 1, high)
}

data := {38, 27, 43, 3, 9, 82, 10, 15, 42, 1}
print("  Before:", joinValues(data))
quicksort(data, 1, #data)
print("  After: ", joinValues(data))
print()

// -------------------------------------------------------
// 2. Merge Sort
// -------------------------------------------------------
print("--- Merge Sort ---")

func mergesort(arr) {
    n := #arr
    if n <= 1 { return arr }

    mid := math.floor(n / 2)
    left := {}
    right := {}

    for i := 1; i <= mid; i++ {
        table.insert(left, arr[i])
    }
    for i := mid + 1; i <= n; i++ {
        table.insert(right, arr[i])
    }

    left = mergesort(left)
    right = mergesort(right)

    // Merge
    result := {}
    li := 1
    ri := 1
    for li <= #left && ri <= #right {
        if left[li] <= right[ri] {
            table.insert(result, left[li])
            li = li + 1
        } else {
            table.insert(result, right[ri])
            ri = ri + 1
        }
    }
    for li <= #left {
        table.insert(result, left[li])
        li = li + 1
    }
    for ri <= #right {
        table.insert(result, right[ri])
        ri = ri + 1
    }
    return result
}

data2 := {64, 34, 25, 12, 22, 11, 90, 45}
print("  Before:", joinValues(data2))
sorted := mergesort(data2)
print("  After: ", joinValues(sorted))
print()

// -------------------------------------------------------
// 3. Binary Search
// -------------------------------------------------------
print("--- Binary Search ---")

func binarySearch(arr, target) {
    low := 1
    high := #arr

    for low <= high {
        mid := math.floor((low + high) / 2)
        if arr[mid] == target {
            return mid
        } elseif arr[mid] < target {
            low = mid + 1
        } else {
            high = mid - 1
        }
    }
    return -1  // not found
}

sortedArr := {2, 5, 8, 12, 16, 23, 38, 56, 72, 91}
print("  Array:", joinValues(sortedArr))

targets := {23, 72, 1, 56, 100}
for i := 1; i <= #targets; i++ {
    idx := binarySearch(sortedArr, targets[i])
    if idx > 0 {
        print(string.format("  Search %d: found at index %d", targets[i], idx))
    } else {
        print(string.format("  Search %d: not found", targets[i]))
    }
}
print()

// -------------------------------------------------------
// 4. BFS (Breadth-First Search) on adjacency list
// -------------------------------------------------------
print("--- BFS (Breadth-First Search) ---")

func buildGraph() {
    // Adjacency list representation
    graph := {}
    addEdge := func(from, to) {
        if graph[from] == nil { graph[from] = {} }
        if graph[to] == nil { graph[to] = {} }
        table.insert(graph[from], to)
        table.insert(graph[to], from)  // undirected
    }

    addEdge("A", "B")
    addEdge("A", "C")
    addEdge("B", "D")
    addEdge("B", "E")
    addEdge("C", "F")
    addEdge("D", "G")
    addEdge("E", "G")

    return graph
}

func bfs(graph, start) {
    visited := {}
    order := {}
    queue := {start}
    visited[start] = true

    for #queue > 0 {
        // Dequeue
        current := queue[1]
        table.remove(queue, 1)
        table.insert(order, current)

        // Visit neighbors
        neighbors := graph[current]
        if neighbors != nil {
            for i := 1; i <= #neighbors; i++ {
                neighbor := neighbors[i]
                if !visited[neighbor] {
                    visited[neighbor] = true
                    table.insert(queue, neighbor)
                }
            }
        }
    }
    return order
}

graph := buildGraph()
bfsOrder := bfs(graph, "A")
print("  Graph: A-B, A-C, B-D, B-E, C-F, D-G, E-G")
print("  BFS from A:", table.concat(bfsOrder, " -> "))
print()

// -------------------------------------------------------
// 5. DFS (Depth-First Search) on adjacency list
// -------------------------------------------------------
print("--- DFS (Depth-First Search) ---")

func dfs(graph, start) {
    visited := {}
    order := {}

    func dfsVisit(node) {
        if visited[node] { return nil }
        visited[node] = true
        table.insert(order, node)

        neighbors := graph[node]
        if neighbors != nil {
            for i := 1; i <= #neighbors; i++ {
                dfsVisit(neighbors[i])
            }
        }
    }

    dfsVisit(start)
    return order
}

dfsOrder := dfs(graph, "A")
print("  DFS from A:", table.concat(dfsOrder, " -> "))

// DFS with iterative approach using a stack
func dfsIterative(graph, start) {
    visited := {}
    order := {}
    stack := {start}

    for #stack > 0 {
        // Pop from stack
        current := stack[#stack]
        table.remove(stack, #stack)

        if !visited[current] {
            visited[current] = true
            table.insert(order, current)

            neighbors := graph[current]
            if neighbors != nil {
                // Push neighbors in reverse order
                for i := #neighbors; i >= 1; i-- {
                    if !visited[neighbors[i]] {
                        table.insert(stack, neighbors[i])
                    }
                }
            }
        }
    }
    return order
}

dfsOrder2 := dfsIterative(graph, "A")
print("  DFS iterative from A:", table.concat(dfsOrder2, " -> "))
print()

// -------------------------------------------------------
// 6. Dijkstra's Shortest Path
// -------------------------------------------------------
print("--- Dijkstra's Shortest Path ---")

func dijkstra(graph, start) {
    // graph is a table: graph[node] = {{to: "B", weight: 5}, ...}
    dist := {}
    prev := {}
    visited := {}

    // Initialize distances to infinity
    for node, _ := range graph {
        dist[node] = math.huge
    }
    dist[start] = 0

    for true {
        // Find unvisited node with minimum distance
        minDist := math.huge
        minNode := nil
        for node, d := range dist {
            if !visited[node] && d < minDist {
                minDist = d
                minNode = node
            }
        }

        if minNode == nil { break }
        visited[minNode] = true

        // Update distances to neighbors
        edges := graph[minNode]
        if edges != nil {
            for i := 1; i <= #edges; i++ {
                edge := edges[i]
                newDist := dist[minNode] + edge.weight
                if newDist < dist[edge.to] {
                    dist[edge.to] = newDist
                    prev[edge.to] = minNode
                }
            }
        }
    }

    return dist, prev
}

func getPath(prev, start, target) {
    path := {}
    current := target
    for current != nil {
        table.insert(path, 1, current)
        if current == start { break }
        current = prev[current]
    }
    return path
}

// Build weighted graph
wgraph := {
    A: {{to: "B", weight: 4}, {to: "C", weight: 2}},
    B: {{to: "D", weight: 3}, {to: "C", weight: 1}},
    C: {{to: "B", weight: 1}, {to: "D", weight: 5}, {to: "E", weight: 7}},
    D: {{to: "E", weight: 2}},
    E: {}
}

dist, prev := dijkstra(wgraph, "A")
print("  Weighted graph:")
print("    A->B(4), A->C(2), B->D(3), B->C(1), C->B(1), C->D(5), C->E(7), D->E(2)")
print("  Shortest distances from A:")

// Collect and sort nodes
nodes := {}
for node, d := range dist {
    table.insert(nodes, {node: node, dist: d})
}
table.sort(nodes, func(a, b) { return a.node < b.node })

for i := 1; i <= #nodes; i++ {
    entry := nodes[i]
    path := getPath(prev, "A", entry.node)
    pathStr := table.concat(path, " -> ")
    print(string.format("    A -> %s: distance=%d, path=%s", entry.node, entry.dist, pathStr))
}
print()

// -------------------------------------------------------
// 7. Fibonacci (iterative + memoized)
// -------------------------------------------------------
print("--- Fibonacci ---")

// Iterative fibonacci
func fibIterative(n) {
    if n < 2 { return n }
    a := 0
    b := 1
    for i := 2; i <= n; i++ {
        a, b = b, a + b
    }
    return b
}

print("  Iterative fibonacci:")
parts := {}
for i := 0; i <= 20; i++ {
    table.insert(parts, tostring(fibIterative(i)))
}
print("  " .. table.concat(parts, ", "))

// Memoized fibonacci
func makeMemoFib() {
    cache := {}
    func fib(n) {
        key := tostring(n)
        if cache[key] != nil { return cache[key] }
        result := 0
        if n < 2 {
            result = n
        } else {
            result = fib(n - 1) + fib(n - 2)
        }
        cache[key] = result
        return result
    }
    return fib
}

memoFib := makeMemoFib()
print("  Memoized fibonacci:")
parts2 := {}
for i := 0; i <= 20; i++ {
    table.insert(parts2, tostring(memoFib(i)))
}
print("  " .. table.concat(parts2, ", "))
print()

// -------------------------------------------------------
// 8. Summary: sorting benchmark
// -------------------------------------------------------
print("--- Sorting Comparison ---")

// Generate a random-ish array
func generateArray(n) {
    arr := {}
    val := n * 7
    for i := 1; i <= n; i++ {
        val = (val * 31 + 17) % 1000
        table.insert(arr, val)
    }
    return arr
}

arr := generateArray(20)
print("  Original:", joinValues(arr))

qsArr := copyTable(arr)
quicksort(qsArr, 1, #qsArr)
print("  Quicksort:", joinValues(qsArr))

msArr := mergesort(copyTable(arr))
print("  Mergesort:", joinValues(msArr))

// Verify both produce same result
match := true
for i := 1; i <= #qsArr; i++ {
    if qsArr[i] != msArr[i] { match = false }
}
print("  Results match:", match)
print()

print("=== Done ===")
