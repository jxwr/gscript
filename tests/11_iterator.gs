// ipairs
t := {"a", "b", "c", "d"}
for i, v := range ipairs(t) {
    print(i, v)
}

// pairs with sorted keys
m := {x: 1, y: 2, z: 3}
keys := {}
for k, v := range pairs(m) {
    table.insert(keys, k)
}
table.sort(keys)
for _, k := range keys {
    print(k, m[k])
}
