// Structural variant: renamed Ackermann-like nested recurrence.
// Tests raw-int nested recursion without relying on the original ack name or
// the original n == 0 argument literal.

func nestwave(level, width) {
    if level == 0 { return width + 2 }
    if width == 0 { return nestwave(level - 1, 2) }
    return nestwave(level - 1, nestwave(level, width - 1))
}

REPS := 60000
t0 := time.now()
result := 0
checksum := 0
for r := 1; r <= REPS; r++ {
    result = nestwave(2, 6)
    checksum = checksum + (result % 997)
}
elapsed := time.since(t0)

print(string.format("nestwave(2,6) = %d (%d reps)", result, REPS))
print(string.format("checksum = %d", checksum))
print(string.format("Time: %.3fs", elapsed))
