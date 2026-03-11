package runtime

// registerStdlibExtra registers additional standard library tables (regexp, utf8, bit32).
// This is called from New() after registerStdlib().
func (interp *Interpreter) registerStdlibExtra() {
	// Regexp library
	interp.globals.Define("regexp", TableValue(buildRegexpLib()))

	// UTF-8 library
	interp.globals.Define("utf8", TableValue(buildUTF8Lib()))

	// Bit32 library
	interp.globals.Define("bit32", TableValue(buildBit32Lib()))
}
