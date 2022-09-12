package ux

import (
	"context"
	"time"

	"github.com/theckman/yacspin"
)

type progressStep struct {
	progress        *Progress
	spinner         *yacspin.Spinner
	executeFn       ProgressStepFn
	continueOnError bool
}

func (s *progressStep) ContinueOnError() bool {
	return s.continueOnError
}

func (s *progressStep) Execute(ctx context.Context, stepCtx StepContext) error {
	s.spinner.Start()

	err := s.executeFn(ctx, stepCtx, s.progress)
	if err != nil {
		s.spinner.StopFail()
		return err
	}

	s.spinner.Stop()
	return nil
}

func NewProgressStep(prefix string, message string, executeFn ProgressStepFn) Step {
	config := yacspin.Config{
		Frequency:         200 * time.Millisecond,
		CharSet:           yacspin.CharSets[33],
		Suffix:            " " + prefix,
		Message:           message,
		SuffixAutoColon:   true,
		StopCharacter:     "(✓) Done",
		StopColors:        []string{"fgGreen"},
		StopFailCharacter: "(x) Error",
		StopFailColors:    []string{"fgRed"},
	}

	spinner, _ := yacspin.New(config)
	progress := &Progress{
		spinner: spinner,
	}

	return &progressStep{
		progress:  progress,
		spinner:   spinner,
		executeFn: executeFn,
	}
}
