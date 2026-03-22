package gscript

// LibFlags controls which standard libraries are loaded.
type LibFlags uint64

const (
	LibString  LibFlags = 1 << iota // string.*
	LibTable                        // table.*
	LibMath                         // math.*
	LibIO                           // io.*
	LibOS                           // os.*
	LibCoroutine                    // coroutine (built-in, always available)
	LibHTTP                         // http.* (server)
	LibGL                           // gl.* (OpenGL)
	LibJSON                         // json.*
	LibBase64                       // base64.*
	LibHash                         // hash.*
	LibFS                           // fs.*
	LibPath                         // path.*
	LibTime                         // time.*
	LibNet                          // net.* (HTTP client)
	LibVec                          // vec.* (2D/3D vectors)
	LibColor                        // color.*
	LibRegexp                       // regexp.*
	LibUTF8                         // utf8.*
	LibBit32                        // bit32.*

	// LibAll includes every library (default).
	LibAll = LibString | LibTable | LibMath | LibIO | LibOS | LibCoroutine |
		LibHTTP | LibGL | LibJSON | LibBase64 | LibHash |
		LibFS | LibPath | LibTime | LibNet |
		LibVec | LibColor | LibRegexp | LibUTF8 | LibBit32

	// LibSafe is a sandboxed subset with no I/O, network, or system access.
	LibSafe = LibString | LibTable | LibMath | LibCoroutine |
		LibJSON | LibBase64 | LibHash | LibVec | LibColor |
		LibRegexp | LibUTF8 | LibBit32

	// LibApp is a convenient preset for application development (no GL).
	LibApp = LibString | LibTable | LibMath | LibIO | LibOS | LibCoroutine |
		LibJSON | LibBase64 | LibHash | LibFS | LibPath | LibTime | LibNet |
		LibRegexp | LibUTF8 | LibBit32

	// LibGame is a preset for game development (no I/O, includes GL/vec/color).
	LibGame = LibString | LibTable | LibMath | LibCoroutine |
		LibGL | LibVec | LibColor | LibJSON | LibBit32 | LibTime
)

type vmOptions struct {
	libs        LibFlags
	requirePath string
	maxSteps    int64
	printFunc   func(args ...interface{})
	useVM       bool // use bytecode VM instead of tree-walker
	useJIT      bool // enable JIT compilation (implies useVM)
}

// Option configures a VM instance.
type Option func(*vmOptions)

// WithLibs sets which standard libraries are available.
// Default: LibAll
func WithLibs(libs LibFlags) Option {
	return func(o *vmOptions) { o.libs = libs }
}

// WithRequirePath sets the base directory for require() module loading.
func WithRequirePath(path string) Option {
	return func(o *vmOptions) { o.requirePath = path }
}

// WithPrint overrides the print() function (useful to capture output in tests/games).
func WithPrint(fn func(args ...interface{})) Option {
	return func(o *vmOptions) { o.printFunc = fn }
}

// WithVM enables the bytecode VM instead of the default tree-walking interpreter.
func WithVM() Option {
	return func(o *vmOptions) { o.useVM = true }
}

// WithJIT enables the ARM64 JIT compiler (implies bytecode VM).
// Only available on darwin/arm64 (Apple Silicon).
func WithJIT() Option {
	return func(o *vmOptions) {
		o.useVM = true
		o.useJIT = true
	}
}

// WithTracing is an alias for WithJIT (kept for backward compatibility).
// The JIT compiler now includes both method-level and trace-level compilation.
func WithTracing() Option {
	return WithJIT()
}
