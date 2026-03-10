// Array-style tables
arr := {10, 20, 30, 40, 50}
print(#arr)
print(arr[1])
print(arr[3])

// Insert/remove
table.insert(arr, 60)
print(#arr)
table.remove(arr, 1)
print(arr[1])

// Hash-style tables
person := {name: "alice", age: 30}
print(person.name)
print(person["age"])

// Nested tables
matrix := {{1,2,3},{4,5,6},{7,8,9}}
print(matrix[2][2])
