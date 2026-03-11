# fs -- File System Library

The `fs` library provides functions for interacting with the local file system: reading and writing files, creating directories, listing directory contents, and more.

All functions that can fail follow the GScript multiple-return convention: on success they return the result value, and on error they return `nil` (or `false`) followed by an error message string.

## Functions

### fs.exists(path) -> bool

Returns `true` if the given path exists (file or directory), `false` otherwise.

```lua
if fs.exists("/tmp/myfile.txt") {
    print("file exists")
}
```

### fs.isfile(path) -> bool

Returns `true` if the path exists and is a regular file.

```lua
if fs.isfile("data.csv") {
    print("it's a file")
}
```

### fs.isdir(path) -> bool

Returns `true` if the path exists and is a directory.

```lua
if fs.isdir("/tmp") {
    print("it's a directory")
}
```

### fs.stat(path) -> table | nil, errMsg

Returns a table with file/directory information, or `nil` and an error message on failure.

The returned table has the following fields:

| Field    | Type   | Description                                |
|----------|--------|--------------------------------------------|
| `name`   | string | Base name of the file                      |
| `size`   | int    | Size in bytes                              |
| `mtime`  | float  | Last modification time (Unix timestamp)    |
| `isdir`  | bool   | Whether the path is a directory            |
| `isfile` | bool   | Whether the path is a regular file         |
| `mode`   | string | Permission mode as octal string, e.g. "0644" |

```lua
info := fs.stat("myfile.txt")
if info != nil {
    print("size:", info.size)
    print("modified:", info.mtime)
}
```

### fs.readfile(path) -> string | nil, errMsg

Reads the entire contents of a file and returns it as a string.

```lua
content, err := fs.readfile("config.json")
if content == nil {
    print("error:", err)
}
```

### fs.writefile(path, content) -> true | nil, errMsg

Writes the string `content` to the file at `path`, creating it if necessary and truncating it if it exists. File permissions default to `0644`.

```lua
ok, err := fs.writefile("output.txt", "hello world")
if ok == nil {
    print("write error:", err)
}
```

### fs.appendfile(path, content) -> true | nil, errMsg

Appends the string `content` to the file at `path`, creating it if necessary.

```lua
fs.appendfile("log.txt", "new log line\n")
```

### fs.remove(path) -> true | nil, errMsg

Removes a file or an empty directory.

```lua
ok, err := fs.remove("temp.txt")
```

### fs.removeAll(path) -> true | nil, errMsg

Removes a path and all its contents recursively (like `rm -rf`).

```lua
fs.removeAll("/tmp/build_output")
```

### fs.rename(oldpath, newpath) -> true | nil, errMsg

Renames (moves) a file or directory.

```lua
fs.rename("old_name.txt", "new_name.txt")
```

### fs.mkdir(path) -> true | nil, errMsg

Creates a single directory. The parent directory must already exist.

```lua
fs.mkdir("/tmp/mydir")
```

### fs.mkdirAll(path) -> true | nil, errMsg

Creates a directory and all necessary parent directories.

```lua
fs.mkdirAll("/tmp/a/b/c")
```

### fs.readdir(path) -> table | nil, errMsg

Returns an array (1-indexed table) of entries in the given directory. Each entry is a table with fields:

| Field   | Type   | Description                    |
|---------|--------|--------------------------------|
| `name`  | string | Name of the entry              |
| `isdir` | bool   | Whether it is a directory      |
| `size`  | int    | Size in bytes (for files)      |

```lua
entries := fs.readdir(".")
for i := 1; i <= #entries; i++ {
    print(entries[i].name, entries[i].isdir)
}
```

### fs.glob(pattern) -> table | nil, errMsg

Returns an array of file paths matching the glob pattern (uses Go's `filepath.Glob`).

```lua
files := fs.glob("*.txt")
for i := 1; i <= #files; i++ {
    print(files[i])
}
```

### fs.copy(src, dst) -> true | nil, errMsg

Copies the contents of the file at `src` to `dst`. Creates `dst` if it does not exist.

```lua
fs.copy("original.txt", "backup.txt")
```

### fs.tempdir() -> string

Returns the default directory for temporary files (e.g. `/tmp` on Unix).

```lua
tmp := fs.tempdir()
print("temp directory:", tmp)
```

### fs.tempfile([dir [, prefix]]) -> string | nil, errMsg

Creates a new temporary file and returns its path. The file is created but closed; you can write to it with `fs.writefile`.

- `dir` (optional): directory in which to create the file. Defaults to `os.TempDir()`.
- `prefix` (optional): prefix for the temp file name.

```lua
p, err := fs.tempfile("/tmp", "myapp_")
if p != nil {
    fs.writefile(p, "temp data")
}
```

### fs.cwd() -> string | nil, errMsg

Returns the current working directory.

```lua
print("working directory:", fs.cwd())
```

### fs.chdir(path) -> true | nil, errMsg

Changes the current working directory.

```lua
fs.chdir("/tmp")
print("now in:", fs.cwd())
```
