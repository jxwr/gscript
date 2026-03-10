// if/elseif/else
x := 15
if x > 20 {
    print("big")
} elseif x > 10 {
    print("medium")
} else {
    print("small")
}

// C-style for loop
sum := 0
for i := 1; i <= 10; i++ {
    sum = sum + i
}
print(sum)

// while-style for loop
n := 1
for n < 100 {
    n = n * 2
}
print(n)
