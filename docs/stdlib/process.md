# process

The `process` library provides functions for running external commands and interacting with the operating system process environment.

## Functions

### process.run(cmd [, opts]) -> table

Run an external command and return a result table with `{ok, stdout, stderr, code}`.

`cmd` can be a string (split by spaces) or a table of arguments.

Options table:
- `stdin` (string) -- string to pass as standard input
- `env` (table) -- additional environment variables `{KEY: "value"}`
- `dir` (string) -- working directory
- `timeout` (number) -- timeout in seconds

```
result := process.run("echo hello")
-- result.ok == true
-- result.stdout == "hello\n"
-- result.stderr == ""
-- result.code == 0

result := process.run({"ls", "-la"})
result := process.run("cat", {stdin: "hello"})
```

### process.exec(cmd, ...) -> string [, error]

Run a command with arguments and return stdout as a string. On failure, returns `nil, "error message"`.

```
out := process.exec("echo", "hello")
-- out == "hello\n"

out, err := process.exec("nonexistent")
-- out == nil, err == "exec: ..."
```

### process.shell(cmd) -> table

Run a command via `/bin/sh -c` and return a result table with `{ok, stdout, stderr, code}`.

```
result := process.shell("echo hello && echo world")
-- result.ok == true
-- result.stdout contains "hello" and "world"
```

### process.which(name) -> string | nil

Find an executable in PATH. Returns the full path or nil if not found.

```
path := process.which("ls")
-- path == "/bin/ls" (or similar)

path := process.which("nonexistent")
-- path == nil
```

### process.pid() -> int

Return the current process ID.

```
pid := process.pid()
```

### process.env() -> table

Return a table of all environment variables as key-value pairs.

```
env := process.env()
path := env.PATH
```
