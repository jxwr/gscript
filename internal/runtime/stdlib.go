package runtime

// registerStdlib registers all standard library tables as globals.
// This is called from New() after registerBuiltins().
func (interp *Interpreter) registerStdlib() {
	// String library
	strLib := buildStringLib()
	interp.globals.Define("string", TableValue(strLib))

	// Set up string metatable so "hello":upper() works
	interp.stringMeta = NewTable()
	interp.stringMeta.RawSet(StringValue("__index"), TableValue(strLib))

	// Table library (sort + higher-order functions need interp)
	tblLib := buildTableLib()
	buildTableSortWithInterp(interp, tblLib)
	buildTableHigherOrderWithInterp(interp, tblLib)
	interp.globals.Define("table", TableValue(tblLib))

	// Math library
	interp.globals.Define("math", TableValue(buildMathLib()))

	// IO library
	interp.globals.Define("io", TableValue(buildIOLib()))

	// OS library
	interp.globals.Define("os", TableValue(buildOSLib()))

	// HTTP server library
	interp.globals.Define("http", TableValue(httpLib(interp)))

	// Raylib game library (window, drawing, input, audio)
	interp.globals.Define("rl", TableValue(rlLib(interp)))

	// --- Encoding / Crypto ---
	interp.globals.Define("json", TableValue(buildJSONLib()))
	interp.globals.Define("base64", TableValue(buildBase64Lib()))
	interp.globals.Define("hash", TableValue(buildHashLib()))

	// --- File system & paths ---
	interp.globals.Define("fs", TableValue(buildFSLib()))
	interp.globals.Define("path", TableValue(buildPathLib()))

	// --- Time & networking ---
	interp.globals.Define("time", TableValue(buildTimeLib()))
	interp.globals.Define("net", TableValue(buildNetLib()))

	// --- System ---
	interp.globals.Define("process", TableValue(buildProcessLib()))

	// --- Data formats ---
	interp.globals.Define("csv", TableValue(buildCSVLib()))
	interp.globals.Define("url", TableValue(buildURLLib()))

	// --- Utilities ---
	interp.globals.Define("uuid", TableValue(buildUUIDLib()))
	interp.globals.Define("bytes", TableValue(buildBytesLib()))

	// --- Game math ---
	interp.globals.Define("vec", TableValue(buildVecLib()))
	interp.globals.Define("color", TableValue(buildColorLib()))

	// --- Text processing ---
	interp.globals.Define("regexp", TableValue(buildRegexpLib()))
	interp.globals.Define("utf8", TableValue(buildUTF8Lib()))

	// --- Low-level ---
	interp.globals.Define("bit32", TableValue(buildBit32Lib()))

	// --- Random number generation ---
	interp.globals.Define("rand", TableValue(buildRandLib()))

	// --- Sorting utilities ---
	interp.globals.Define("sort", TableValue(buildSortLib(interp)))

	// --- Encoding utilities ---
	interp.globals.Define("encoding", TableValue(buildEncodingLib()))

	// --- Compression ---
	interp.globals.Define("compress", TableValue(buildCompressLib()))

	// --- Cryptography ---
	interp.globals.Define("crypto", TableValue(buildCryptoLib()))

	// --- Container data structures ---
	interp.globals.Define("container", TableValue(buildContainerLib(interp)))

	// --- Logging ---
	interp.globals.Define("log", TableValue(buildLogLib()))
}
