package methodjit

func tier2StringNativeModules() []Tier2OptimizerModule {
	return []Tier2OptimizerModule{
		{
			Name:  "StringNativeCleanup",
			Phase: Tier2PhaseStringNative,
			RunWithContext: func(fn *Function, opts *Tier2PipelineOpts, ctx *Tier2OptimizerContext) (*Function, error) {
				out, notes := StringNativeCleanupPass(fn)
				if ctx != nil && len(notes) > 0 {
					ctx.IntrinsicNotes = append(ctx.IntrinsicNotes, notes...)
				}
				return out, nil
			},
		},
	}
}
