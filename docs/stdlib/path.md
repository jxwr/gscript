# path -- Path Manipulation Library

The `path` library provides functions for manipulating file paths in an OS-independent way. All path operations use the host operating system's path conventions.

## Constants

### path.separator

The OS-specific path separator character as a string: `"/"` on Unix, `"\\"` on Windows.

### path.listSeparator

The OS-specific list separator character as a string: `":"` on Unix, `";"` on Windows.

## Functions

### path.join(...) -> string

Joins any number of path elements into a single path, separated by the OS path separator. Empty elements are ignored. The result is cleaned (see `path.clean`).

```lua
p := path.join("usr", "local", "bin")
// Unix: "usr/local/bin"
```

### path.dir(p) -> string

Returns the parent directory of the path (everything before the last separator).

```lua
path.dir("/usr/local/bin/go")  // "/usr/local/bin"
path.dir("file.txt")           // "."
```

### path.base(p) -> string

Returns the last element of the path (the file or directory name).

```lua
path.base("/usr/local/bin/go")  // "go"
path.base("/")                  // "/"
```

### path.ext(p) -> string

Returns the file extension, including the leading dot. Returns an empty string if there is no extension.

```lua
path.ext("main.go")         // ".go"
path.ext("archive.tar.gz")  // ".gz"
path.ext("Makefile")        // ""
```

### path.abs(p) -> string | nil, errMsg

Returns the absolute path. If the path is not absolute, it is joined with the current working directory.

```lua
abs := path.abs(".")
print(abs)  // e.g. "/home/user/project"
```

### path.isAbs(p) -> bool

Returns `true` if the path is absolute.

```lua
path.isAbs("/usr/bin")      // true
path.isAbs("relative/dir")  // false
```

### path.clean(p) -> string

Returns the shortest path name equivalent to the given path by applying the following rules:
- Replace multiple separators with a single one
- Eliminate `.` and `..` elements
- Eliminate internal `..` after non-`..` elements

```lua
path.clean("/usr//local/../local/bin/./go")  // "/usr/local/bin/go"
```

### path.split(p) -> dir, file

Splits a path into its directory and file components. Returns two strings.

```lua
dir, file := path.split("/usr/local/bin/go")
// dir = "/usr/local/bin/", file = "go"
```

### path.match(pattern, name) -> bool, errMsg

Reports whether the name matches the shell glob pattern. On pattern syntax error, returns `false` and an error message.

Supported pattern syntax:
- `*` matches any sequence of non-separator characters
- `?` matches any single non-separator character
- `[...]` matches character ranges

```lua
path.match("*.go", "main.go")   // true
path.match("*.go", "main.txt")  // false
```

### path.rel(basepath, targpath) -> string | nil, errMsg

Returns a relative path from `basepath` to `targpath`. Returns `nil` and an error message if the paths cannot be made relative.

```lua
path.rel("/usr/local", "/usr/local/bin/go")  // "bin/go"
```
