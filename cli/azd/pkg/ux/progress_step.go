package ux

import (
	"context"
	"fmt"
	"time"

	"github.com/theckman/yacspin"
)

type progressStep struct {
	prefix          string
	message         string
	progress        *Progress
	spinner         *yacspin.Spinner
	executeFn       ProgressStepFn
	continueOnError bool
	indent          string
	initialized     bool
}

func (s *progressStep) ContinueOnError() bool {
	return s.continueOnError
}

func (s *progressStep) Initialize() error {
	if s.initialized {
		return nil
	}

	// Defines a custom charset animation
	customCharSet := []string{"|       |", "|=      |", "|==     |", "|===    |", "|====   |", "|=====  |", "|====== |", "|=======|"}
	newCharSet := make([]string, len(customCharSet))
	for i, value := range customCharSet {
		newCharSet[i] = fmt.Sprintf("%s%s", s.indent, value)
	}

	config := yacspin.Config{
		Frequency:         200 * time.Millisecond,
		CharSet:           newCharSet,
		Suffix:            s.prefix,
		Message:           s.message,
		SuffixAutoColon:   true,
		StopCharacter:     fmt.Sprintf("%s(✓) Done:", s.indent),
		StopColors:        []string{"fgGreen"},
		StopFailCharacter: fmt.Sprintf("%s(x) Failed:", s.indent),
		StopFailColors:    []string{"fgRed"},
	}

	spinner, err := yacspin.New(config)
	if err != nil {
		return fmt.Errorf("failed creating spinner: %w", err)
	}

	s.spinner = spinner
	s.progress = &Progress{
		spinner: spinner,
		indent:  s.indent,
	}

	s.initialized = true

	return nil
}

func (s *progressStep) Execute(ctx context.Context, stepCtx StepContext) error {
	s.Initialize()
	s.spinner.Start()

	err := s.executeFn(ctx, stepCtx, s.progress)
	if err != nil {
		s.spinner.StopFail()
		return err
	}

	s.spinner.Stop()
	return nil
}

func (s *progressStep) SetIndent(value string) {
	s.indent = value
}

func NewProgressStep(prefix string, message string, executeFn ProgressStepFn) Step {
	return &progressStep{
		prefix:    prefix,
		message:   message,
		executeFn: executeFn,
	}
}
