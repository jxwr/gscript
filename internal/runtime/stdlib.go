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

	// Table library
	tblLib := buildTableLib()
	buildTableSortWithInterp(interp, tblLib)
	interp.globals.Define("table", TableValue(tblLib))

	// Math library
	interp.globals.Define("math", TableValue(buildMathLib()))

	// IO library
	interp.globals.Define("io", TableValue(buildIOLib()))

	// OS library
	interp.globals.Define("os", TableValue(buildOSLib()))

	// HTTP library
	interp.globals.Define("http", TableValue(httpLib(interp)))

	// GL library (OpenGL + GLFW)
	interp.globals.Define("gl", TableValue(glLib(interp)))

	// JSON library
	interp.globals.Define("json", TableValue(buildJSONLib()))

	// Base64 library
	interp.globals.Define("base64", TableValue(buildBase64Lib()))

	// Hash library
	interp.globals.Define("hash", TableValue(buildHashLib()))

	// File system library
	interp.globals.Define("fs", TableValue(buildFSLib()))

	// Path library
	interp.globals.Define("path", TableValue(buildPathLib()))

	// Time library
	interp.globals.Define("time", TableValue(buildTimeLib()))

	// Net library (HTTP client)
	interp.globals.Define("net", TableValue(buildNetLib()))

	// Vec library (2D/3D vectors)
	interp.globals.Define("vec", TableValue(buildVecLib()))

	// Color library
	interp.globals.Define("color", TableValue(buildColorLib()))

	// Regexp library
	interp.globals.Define("regexp", TableValue(buildRegexpLib()))

	// UTF-8 library
	interp.globals.Define("utf8", TableValue(buildUTF8Lib()))

	// Bit32 library
	interp.globals.Define("bit32", TableValue(buildBit32Lib()))
}
