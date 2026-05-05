//go:build darwin && arm64

package methodjit

import "time"

type tier2CompileStage struct {
	name string
	run  func() error
}

func runTier2CompileStages(trace *Tier2Trace, stages []tier2CompileStage) error {
	for _, stage := range stages {
		if trace == nil {
			if err := stage.run(); err != nil {
				return err
			}
			continue
		}
		start := time.Now()
		err := stage.run()
		trace.PipelineStages = append(trace.PipelineStages, newPipelineStageTiming(stage.name, time.Since(start), err))
		if err != nil {
			return err
		}
	}
	return nil
}
