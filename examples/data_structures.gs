// data_structures.gs - Data structures implemented in GScript
// Demonstrates: stack, queue, linked list, binary search tree, hash set, priority queue

print("=== Data Structures in GScript ===")
print()

// -------------------------------------------------------
// 1. Stack (LIFO) - push, pop, peek, isEmpty
// -------------------------------------------------------
print("--- Stack ---")

func newStack() {
    items := {}
    stack := {}

    stack.push = func(val) {
        table.insert(items, val)
    }

    stack.pop = func() {
        if #items == 0 { return nil }
        val := items[#items]
        table.remove(items, #items)
        return val
    }

    stack.peek = func() {
        if #items == 0 { return nil }
        return items[#items]
    }

    stack.isEmpty = func() {
        return #items == 0
    }

    stack.size = func() {
        return #items
    }

    stack.toString = func() {
        parts := {}
        for i := 1; i <= #items; i++ {
            table.insert(parts, tostring(items[i]))
        }
        return "[" .. table.concat(parts, ", ") .. "] <- top"
    }

    return stack
}

s := newStack()
s.push(10)
s.push(20)
s.push(30)
print("  Stack:", s.toString())
print("  Peek:", s.peek())
print("  Pop:", s.pop())
print("  Pop:", s.pop())
print("  Stack:", s.toString())
print("  Size:", s.size())
print()

// -------------------------------------------------------
// 2. Queue (FIFO) - enqueue, dequeue, peek, isEmpty
// -------------------------------------------------------
print("--- Queue ---")

func newQueue() {
    items := {}
    head := 1
    q := {}

    q.enqueue = func(val) {
        table.insert(items, val)
    }

    q.dequeue = func() {
        if head > #items { return nil }
        val := items[head]
        items[head] = nil
        head = head + 1
        return val
    }

    q.peek = func() {
        if head > #items { return nil }
        return items[head]
    }

    q.isEmpty = func() {
        return head > #items
    }

    q.size = func() {
        if head > #items { return 0 }
        return #items - head + 1
    }

    q.toString = func() {
        parts := {}
        for i := head; i <= #items; i++ {
            table.insert(parts, tostring(items[i]))
        }
        return "front -> [" .. table.concat(parts, ", ") .. "]"
    }

    return q
}

q := newQueue()
q.enqueue("Alice")
q.enqueue("Bob")
q.enqueue("Charlie")
print("  Queue:", q.toString())
print("  Dequeue:", q.dequeue())
print("  Peek:", q.peek())
q.enqueue("Diana")
print("  Queue:", q.toString())
print("  Size:", q.size())
print()

// -------------------------------------------------------
// 3. Linked List - insert, delete, traverse
// -------------------------------------------------------
print("--- Linked List ---")

func newLinkedList() {
    list := {}
    head := nil
    size := 0

    // Create a new node
    func makeNode(val) {
        return {value: val, next: nil}
    }

    // Insert at the front
    list.insertFront = func(val) {
        node := makeNode(val)
        node.next = head
        head = node
        size = size + 1
    }

    // Insert at the back
    list.insertBack = func(val) {
        node := makeNode(val)
        if head == nil {
            head = node
        } else {
            current := head
            for current.next != nil {
                current = current.next
            }
            current.next = node
        }
        size = size + 1
    }

    // Delete first occurrence of val
    list.delete = func(val) {
        if head == nil { return false }
        if head.value == val {
            head = head.next
            size = size - 1
            return true
        }
        current := head
        for current.next != nil {
            if current.next.value == val {
                current.next = current.next.next
                size = size - 1
                return true
            }
            current = current.next
        }
        return false
    }

    // Search for a value
    list.contains = func(val) {
        current := head
        for current != nil {
            if current.value == val { return true }
            current = current.next
        }
        return false
    }

    // Get size
    list.size = func() {
        return size
    }

    // Convert to string
    list.toString = func() {
        parts := {}
        current := head
        for current != nil {
            table.insert(parts, tostring(current.value))
            current = current.next
        }
        return table.concat(parts, " -> ") .. " -> nil"
    }

    // Traverse and apply function
    list.forEach = func(fn) {
        current := head
        idx := 0
        for current != nil {
            idx = idx + 1
            fn(idx, current.value)
            current = current.next
        }
    }

    return list
}

ll := newLinkedList()
ll.insertBack(1)
ll.insertBack(2)
ll.insertBack(3)
ll.insertFront(0)
print("  List:", ll.toString())
print("  Contains 2:", ll.contains(2))
print("  Contains 5:", ll.contains(5))
ll.delete(2)
print("  After deleting 2:", ll.toString())
ll.insertBack(4)
print("  After inserting 4:", ll.toString())
print("  Size:", ll.size())
print()

// -------------------------------------------------------
// 4. Binary Search Tree (BST) - insert, search, inorder
// -------------------------------------------------------
print("--- Binary Search Tree ---")

func newBST() {
    bst := {}
    root := nil

    func makeNode(val) {
        return {value: val, left: nil, right: nil}
    }

    func insertNode(node, val) {
        if node == nil {
            return makeNode(val)
        }
        if val < node.value {
            node.left = insertNode(node.left, val)
        } elseif val > node.value {
            node.right = insertNode(node.right, val)
        }
        // Duplicates are ignored
        return node
    }

    func searchNode(node, val) {
        if node == nil { return false }
        if val == node.value { return true }
        if val < node.value {
            return searchNode(node.left, val)
        }
        return searchNode(node.right, val)
    }

    func inorderCollect(node, result) {
        if node == nil { return nil }
        inorderCollect(node.left, result)
        table.insert(result, node.value)
        inorderCollect(node.right, result)
    }

    func findMin(node) {
        for node.left != nil {
            node = node.left
        }
        return node
    }

    func deleteNode(node, val) {
        if node == nil { return nil }
        if val < node.value {
            node.left = deleteNode(node.left, val)
        } elseif val > node.value {
            node.right = deleteNode(node.right, val)
        } else {
            // Found the node to delete
            if node.left == nil { return node.right }
            if node.right == nil { return node.left }
            // Two children: replace with inorder successor
            successor := findMin(node.right)
            node.value = successor.value
            node.right = deleteNode(node.right, successor.value)
        }
        return node
    }

    bst.insert = func(val) {
        root = insertNode(root, val)
    }

    bst.search = func(val) {
        return searchNode(root, val)
    }

    bst.delete = func(val) {
        root = deleteNode(root, val)
    }

    bst.inorder = func() {
        result := {}
        inorderCollect(root, result)
        return result
    }

    bst.toString = func() {
        vals := bst.inorder()
        parts := {}
        for i := 1; i <= #vals; i++ {
            table.insert(parts, tostring(vals[i]))
        }
        return "[" .. table.concat(parts, ", ") .. "]"
    }

    return bst
}

tree := newBST()
values := {5, 3, 7, 1, 4, 6, 8, 2}
for i := 1; i <= #values; i++ {
    tree.insert(values[i])
}
print("  BST inorder:", tree.toString())
print("  Search 4:", tree.search(4))
print("  Search 9:", tree.search(9))

tree.delete(3)
print("  After deleting 3:", tree.toString())
tree.delete(7)
print("  After deleting 7:", tree.toString())
print()

// -------------------------------------------------------
// 5. Hash Set - add, contains, remove
// -------------------------------------------------------
print("--- Hash Set ---")

func newHashSet() {
    data := {}
    count := 0
    set := {}

    set.add = func(val) {
        key := tostring(val)
        if data[key] == nil {
            data[key] = true
            count = count + 1
        }
    }

    set.contains = func(val) {
        key := tostring(val)
        return data[key] == true
    }

    set.remove = func(val) {
        key := tostring(val)
        if data[key] != nil {
            data[key] = nil
            count = count - 1
            return true
        }
        return false
    }

    set.size = func() {
        return count
    }

    set.toTable = func() {
        result := {}
        for k, v := range data {
            if v == true {
                table.insert(result, k)
            }
        }
        return result
    }

    set.toString = func() {
        items := set.toTable()
        table.sort(items)
        return "{" .. table.concat(items, ", ") .. "}"
    }

    // Set operations
    set.union = func(other) {
        result := newHashSet()
        for k, v := range data {
            if v == true { result.add(k) }
        }
        otherItems := other.toTable()
        for i := 1; i <= #otherItems; i++ {
            result.add(otherItems[i])
        }
        return result
    }

    set.intersection = func(other) {
        result := newHashSet()
        for k, v := range data {
            if v == true && other.contains(k) {
                result.add(k)
            }
        }
        return result
    }

    return set
}

setA := newHashSet()
setA.add("apple")
setA.add("banana")
setA.add("cherry")
setA.add("apple")  // duplicate

setB := newHashSet()
setB.add("banana")
setB.add("date")
setB.add("elderberry")

print("  Set A:", setA.toString())
print("  Set B:", setB.toString())
print("  A contains 'banana':", setA.contains("banana"))
print("  A contains 'date':", setA.contains("date"))
print("  A size:", setA.size())

unionSet := setA.union(setB)
print("  A union B:", unionSet.toString())

interSet := setA.intersection(setB)
print("  A intersect B:", interSet.toString())

setA.remove("cherry")
print("  A after removing 'cherry':", setA.toString())
print()

// -------------------------------------------------------
// 6. Priority Queue using table.sort
// -------------------------------------------------------
print("--- Priority Queue ---")

func newPriorityQueue() {
    items := {}
    pq := {}

    pq.push = func(value, priority) {
        table.insert(items, {value: value, priority: priority})
        // Sort by priority (lower = higher priority)
        table.sort(items, func(a, b) { return a.priority < b.priority })
    }

    pq.pop = func() {
        if #items == 0 { return nil }
        val := items[1].value
        table.remove(items, 1)
        return val
    }

    pq.peek = func() {
        if #items == 0 { return nil }
        return items[1].value
    }

    pq.isEmpty = func() {
        return #items == 0
    }

    pq.size = func() {
        return #items
    }

    pq.toString = func() {
        parts := {}
        for i := 1; i <= #items; i++ {
            table.insert(parts, tostring(items[i].value) .. "(p=" .. tostring(items[i].priority) .. ")")
        }
        return "[" .. table.concat(parts, ", ") .. "]"
    }

    return pq
}

pq := newPriorityQueue()
pq.push("low priority task", 10)
pq.push("critical task", 1)
pq.push("medium task", 5)
pq.push("urgent task", 2)
pq.push("normal task", 7)

print("  Priority queue:", pq.toString())
print("  Pop:", pq.pop())
print("  Pop:", pq.pop())
print("  Pop:", pq.pop())
print("  Remaining:", pq.toString())
print()

// -------------------------------------------------------
// 7. Usage example: balanced parentheses checker using stack
// -------------------------------------------------------
print("--- Application: Balanced Parentheses ---")

func isBalanced(str) {
    stack := newStack()
    matching := {[")"]: "(", ["]"]: "[", ["}"]: "{"}

    for i := 1; i <= #str; i++ {
        ch := string.sub(str, i, i)
        if ch == "(" || ch == "[" || ch == "{" {
            stack.push(ch)
        } elseif ch == ")" || ch == "]" || ch == "}" {
            if stack.isEmpty() { return false }
            top := stack.pop()
            if top != matching[ch] { return false }
        }
    }
    return stack.isEmpty()
}

tests := {
    {expr: "(())", expected: true},
    {expr: "({[]})", expected: true},
    {expr: "(()", expected: false},
    {expr: "({)}", expected: false},
    {expr: "", expected: true},
    {expr: "a + (b * [c - {d}])", expected: true}
}

for i := 1; i <= #tests; i++ {
    t := tests[i]
    result := isBalanced(t.expr)
    status := "PASS"
    if result != t.expected { status = "FAIL" }
    print(string.format("  %s: \"%s\" -> %s (expected %s)", status, t.expr, tostring(result), tostring(t.expected)))
}
print()

print("=== Done ===")
