// string_processing.gs - String manipulation patterns in GScript
// Demonstrates: splitting, joining, template rendering, CSV parsing,
//               simple lexer, formatting utilities

print("=== String Processing ===")
print()

// -------------------------------------------------------
// 1. String splitting
// -------------------------------------------------------
print("--- String Splitting ---")

// Split using the built-in string.split
parts := string.split("hello,world,foo,bar", ",")
print("  Split 'hello,world,foo,bar' by ',':")
for i := 1; i <= #parts; i++ {
    print("    [" .. i .. "] = '" .. parts[i] .. "'")
}

// Custom split that handles multiple characters and edge cases
func splitStr(s, sep) {
    result := {}
    if #s == 0 {
        return result
    }
    if #sep == 0 {
        // Split into individual characters
        for i := 1; i <= #s; i++ {
            table.insert(result, string.sub(s, i, i))
        }
        return result
    }

    start := 1
    for true {
        pos := string.find(s, sep, start, true)
        if pos == nil {
            table.insert(result, string.sub(s, start))
            break
        }
        table.insert(result, string.sub(s, start, pos - 1))
        start = pos + #sep
    }
    return result
}

parts2 := splitStr("one::two::three::four", "::")
print("  Split 'one::two::three::four' by '::':")
for i := 1; i <= #parts2; i++ {
    print("    [" .. i .. "] = '" .. parts2[i] .. "'")
}
print()

// -------------------------------------------------------
// 2. String joining
// -------------------------------------------------------
print("--- String Joining ---")

func join(tbl, sep) {
    if #tbl == 0 { return "" }
    result := tostring(tbl[1])
    for i := 2; i <= #tbl; i++ {
        result = result .. sep .. tostring(tbl[i])
    }
    return result
}

words := {"Hello", "World", "from", "GScript"}
print("  Join with ' ':", join(words, " "))
print("  Join with '-':", join(words, "-"))
print("  Join with ', ':", join(words, ", "))

nums := {1, 2, 3, 4, 5}
print("  Join nums:", join(nums, " + ") .. " = 15")
print()

// -------------------------------------------------------
// 3. Template rendering
// -------------------------------------------------------
print("--- Template Rendering ---")

// Simple template engine: replaces {key} with values from a table
func render(template, vars) {
    result := template
    for key, value := range vars {
        placeholder := "{" .. key .. "}"
        result = string.gsub(result, placeholder, tostring(value))
    }
    return result
}

tmpl := "Hello, {name}! You are {age} years old and live in {city}."
data := {name: "Alice", age: 30, city: "Wonderland"}
print("  " .. render(tmpl, data))

tmpl2 := "Dear {recipient}, Your order #{orderId} has been {status}. Thank you, {sender}."
data2 := {recipient: "Bob", orderId: 12345, status: "shipped", sender: "GScript Store"}
print("  " .. render(tmpl2, data2))

// Nested template rendering with multiple passes
tmpl3 := "{greeting}, {name}!"
print("  " .. render(tmpl3, {greeting: "Bonjour", name: "Claude"}))
print()

// -------------------------------------------------------
// 4. CSV Parser
// -------------------------------------------------------
print("--- CSV Parser ---")

func parseCSVLine(line) {
    fields := {}
    current := ""
    inQuotes := false
    i := 1
    for i <= #line {
        ch := string.sub(line, i, i)
        if inQuotes {
            if ch == "\"" {
                // Check for escaped quote
                if i < #line && string.sub(line, i + 1, i + 1) == "\"" {
                    current = current .. "\""
                    i = i + 1
                } else {
                    inQuotes = false
                }
            } else {
                current = current .. ch
            }
        } else {
            if ch == "\"" {
                inQuotes = true
            } elseif ch == "," {
                table.insert(fields, current)
                current = ""
            } else {
                current = current .. ch
            }
        }
        i = i + 1
    }
    table.insert(fields, current)
    return fields
}

func parseCSV(text) {
    lines := splitStr(text, "\n")
    if #lines == 0 { return {} }

    headers := parseCSVLine(lines[1])
    rows := {}

    for i := 2; i <= #lines; i++ {
        if #lines[i] > 0 {
            fields := parseCSVLine(lines[i])
            row := {}
            for j := 1; j <= #headers; j++ {
                if j <= #fields {
                    row[headers[j]] = fields[j]
                } else {
                    row[headers[j]] = ""
                }
            }
            table.insert(rows, row)
        }
    }
    return rows
}

csv := "name,age,city\nAlice,30,New York\nBob,25,San Francisco\nCharlie,35,Chicago"
rows := parseCSV(csv)
print("  Parsed CSV (" .. #rows .. " rows):")
for i := 1; i <= #rows; i++ {
    row := rows[i]
    print(string.format("    %s, age %s, from %s", row.name, row.age, row.city))
}

// CSV with simple fields
csv2 := "product,description,price\nWidget,A useful device,9.99\nGadget,A fancy tool,19.99"
rows2 := parseCSV(csv2)
print("  Parsed second CSV:")
for i := 1; i <= #rows2; i++ {
    row := rows2[i]
    print(string.format("    %s: %s ($%s)", row.product, row.description, row.price))
}
print()

// -------------------------------------------------------
// 5. Simple lexer for arithmetic expressions
// -------------------------------------------------------
print("--- Arithmetic Lexer ---")

func lexArithmetic(input) {
    tokens := {}
    i := 1
    for i <= #input {
        ch := string.sub(input, i, i)

        // Skip whitespace
        if ch == " " || ch == "\t" {
            i = i + 1
        } elseif ch == "+" || ch == "-" || ch == "*" || ch == "/" || ch == "(" || ch == ")" || ch == "^" || ch == "%" {
            // Operator or paren
            table.insert(tokens, {type: "op", value: ch})
            i = i + 1
        } elseif string.find(ch, "%d") != nil || ch == "." {
            // Number: collect digits and dots
            numStr := ""
            for i <= #input {
                c := string.sub(input, i, i)
                if string.find(c, "%d") != nil || c == "." {
                    numStr = numStr .. c
                    i = i + 1
                } else {
                    break
                }
            }
            table.insert(tokens, {type: "number", value: numStr})
        } elseif string.find(ch, "%a") != nil {
            // Identifier: collect letters and digits
            ident := ""
            for i <= #input {
                c := string.sub(input, i, i)
                if string.find(c, "[%w_]") != nil {
                    ident = ident .. c
                    i = i + 1
                } else {
                    break
                }
            }
            table.insert(tokens, {type: "ident", value: ident})
        } else {
            table.insert(tokens, {type: "unknown", value: ch})
            i = i + 1
        }
    }
    return tokens
}

func showTokens(expr) {
    tokens := lexArithmetic(expr)
    parts := {}
    for i := 1; i <= #tokens; i++ {
        t := tokens[i]
        table.insert(parts, t.type .. "(" .. t.value .. ")")
    }
    print("  \"" .. expr .. "\" -> " .. table.concat(parts, " "))
}

showTokens("3 + 4 * 2")
showTokens("(10 - 5) / 2.5")
showTokens("sin(x) + cos(y)")
showTokens("42 * (a + b)")
print()

// -------------------------------------------------------
// 6. String formatting utilities
// -------------------------------------------------------
print("--- String Formatting Utilities ---")

// Left pad a string to a given width
func padLeft(s, width, ch) {
    if ch == nil { ch = " " }
    for #s < width {
        s = ch .. s
    }
    return s
}

// Right pad a string to a given width
func padRight(s, width, ch) {
    if ch == nil { ch = " " }
    for #s < width {
        s = s .. ch
    }
    return s
}

// Center a string within a given width
func center(s, width, ch) {
    if ch == nil { ch = " " }
    for #s < width {
        s = ch .. s
        if #s < width {
            s = s .. ch
        }
    }
    return s
}

// Trim whitespace from both sides
func trim(s) {
    s = string.gsub(s, "^%s+", "")
    s = string.gsub(s, "%s+$", "")
    return s
}

// Repeat a string
func repeatStr(s, n) {
    return string.rep(s, n)
}

print("  padLeft('42', 6, '0'):", "'" .. padLeft("42", 6, "0") .. "'")
print("  padRight('hi', 10, '.'):", "'" .. padRight("hi", 10, ".") .. "'")
print("  center('title', 20, '-'):", "'" .. center("title", 20, "-") .. "'")
print("  trim('  hello  '):", "'" .. trim("  hello  ") .. "'")
print("  repeat('ha', 5):", "'" .. repeatStr("ha", 5) .. "'")
print()

// Table formatter
func formatTable(headers, rows) {
    // Calculate column widths
    widths := {}
    for i := 1; i <= #headers; i++ {
        widths[i] = #headers[i]
    }
    for i := 1; i <= #rows; i++ {
        for j := 1; j <= #rows[i]; j++ {
            cellLen := #tostring(rows[i][j])
            if j <= #widths {
                if cellLen > widths[j] {
                    widths[j] = cellLen
                }
            }
        }
    }

    // Build separator
    sepParts := {}
    for i := 1; i <= #widths; i++ {
        table.insert(sepParts, repeatStr("-", widths[i] + 2))
    }
    separator := "+" .. table.concat(sepParts, "+") .. "+"

    // Build header
    headerParts := {}
    for i := 1; i <= #headers; i++ {
        table.insert(headerParts, " " .. padRight(headers[i], widths[i]) .. " ")
    }
    headerLine := "|" .. table.concat(headerParts, "|") .. "|"

    // Print
    print("  " .. separator)
    print("  " .. headerLine)
    print("  " .. separator)
    for i := 1; i <= #rows; i++ {
        rowParts := {}
        for j := 1; j <= #headers; j++ {
            val := ""
            if j <= #rows[i] {
                val = tostring(rows[i][j])
            }
            table.insert(rowParts, " " .. padRight(val, widths[j]) .. " ")
        }
        print("  |" .. table.concat(rowParts, "|") .. "|")
    }
    print("  " .. separator)
}

formatTable(
    {"Name", "Age", "City"},
    {
        {"Alice", "30", "New York"},
        {"Bob", "25", "San Francisco"},
        {"Charlie", "35", "Chicago"},
        {"Diana", "28", "Boston"}
    }
)
print()

// -------------------------------------------------------
// 7. String byte length (simple length counting)
// -------------------------------------------------------
print("--- String Length ---")

func byteLength(s) {
    return #s
}

strings_to_measure := {"hello", "GScript", "", "a", "hello world"}
for i := 1; i <= #strings_to_measure; i++ {
    s := strings_to_measure[i]
    print(string.format("  \"%s\" -> byte length: %d", s, byteLength(s)))
}
print()

// -------------------------------------------------------
// 8. Word frequency counter
// -------------------------------------------------------
print("--- Word Frequency Counter ---")

func wordFrequency(text) {
    words := string.split(string.lower(text), " ")
    freq := {}
    for i := 1; i <= #words; i++ {
        word := words[i]
        if #word > 0 {
            if freq[word] == nil {
                freq[word] = 0
            }
            freq[word] = freq[word] + 1
        }
    }
    return freq
}

text := "the quick brown fox jumps over the lazy dog the fox the dog"
freq := wordFrequency(text)
print("  Text: \"" .. text .. "\"")
print("  Word frequencies:")
// Collect and sort
freqList := {}
for word, count := range freq {
    table.insert(freqList, {word: word, count: count})
}
table.sort(freqList, func(a, b) { return a.count > b.count })
for i := 1; i <= #freqList; i++ {
    entry := freqList[i]
    bar := repeatStr("#", entry.count)
    print(string.format("    %-8s %s (%d)", entry.word, bar, entry.count))
}
print()

print("=== Done ===")
