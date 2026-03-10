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
}
